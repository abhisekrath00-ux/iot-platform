// OIDC single sign-on: authorization-code flow against any compliant
// provider (Okta, Entra ID, Google Workspace, Keycloak). Discovery is live,
// id_token signatures are verified against the provider JWKS, and state +
// nonce protect the callback against CSRF and token replay.
package auth

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

type OIDCProvider struct {
	Issuer, ClientID, ClientSecret, RedirectURL string
	authzURL, tokenURL, jwksURL                 string
	http                                        *http.Client
}

type OIDCIdentity struct {
	Email, Name string
}

var ErrOIDCDisabled = errors.New("oidc not configured")

// NewOIDCProvider runs live discovery; call at startup and fail fast.
func NewOIDCProvider(ctx context.Context, issuer, clientID, clientSecret, redirectURL string) (*OIDCProvider, error) {
	p := &OIDCProvider{Issuer: strings.TrimSuffix(issuer, "/"), ClientID: clientID, ClientSecret: clientSecret,
		RedirectURL: redirectURL, http: &http.Client{Timeout: 10 * time.Second}}
	if issuer == "" {
		return nil, ErrOIDCDisabled
	}
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, p.Issuer+"/.well-known/openid-configuration", nil)
	resp, err := p.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("oidc discovery: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("oidc discovery: status %d", resp.StatusCode)
	}
	var meta struct {
		AuthorizationEndpoint string `json:"authorization_endpoint"`
		TokenEndpoint         string `json:"token_endpoint"`
		JWKSURI               string `json:"jwks_uri"`
		Issuer                string `json:"issuer"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&meta); err != nil {
		return nil, fmt.Errorf("oidc discovery: %w", err)
	}
	if meta.AuthorizationEndpoint == "" || meta.TokenEndpoint == "" || meta.JWKSURI == "" {
		return nil, fmt.Errorf("oidc discovery: incomplete metadata")
	}
	if meta.Issuer != "" && meta.Issuer != p.Issuer {
		return nil, fmt.Errorf("oidc discovery: issuer mismatch %q != %q", meta.Issuer, p.Issuer)
	}
	p.authzURL, p.tokenURL, p.jwksURL = meta.AuthorizationEndpoint, meta.TokenEndpoint, meta.JWKSURI
	return p, nil
}

// RandomToken returns 256 bits of URL-safe entropy for state and nonce.
func RandomToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

func (p *OIDCProvider) AuthURL(state, nonce string) string {
	q := url.Values{
		"client_id":     {p.ClientID},
		"redirect_uri":  {p.RedirectURL},
		"response_type": {"code"},
		"scope":         {"openid email profile"},
		"state":         {state},
		"nonce":         {nonce},
	}
	return p.authzURL + "?" + q.Encode()
}

// Exchange trades the callback code for tokens and verifies the id_token.
func (p *OIDCProvider) Exchange(ctx context.Context, code, expectedNonce string) (*OIDCIdentity, error) {
	form := url.Values{
		"grant_type":   {"authorization_code"},
		"code":         {code},
		"redirect_uri": {p.RedirectURL},
		"client_id":    {p.ClientID},
	}
	if p.ClientSecret != "" {
		form.Set("client_secret", p.ClientSecret)
	}
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, p.tokenURL, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := p.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("token exchange: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("token exchange: status %d", resp.StatusCode)
	}
	var tok struct {
		IDToken string `json:"id_token"`
	}
	if err := json.Unmarshal(body, &tok); err != nil || tok.IDToken == "" {
		return nil, fmt.Errorf("token exchange: no id_token")
	}
	return p.VerifyIDToken(ctx, tok.IDToken, expectedNonce)
}

type oidcClaims struct {
	Email string `json:"email"`
	Name  string `json:"name"`
	Nonce string `json:"nonce"`
	jwt.RegisteredClaims
}

// VerifyIDToken checks RS256 signature (JWKS by kid), issuer, audience,
// expiry, and nonce, then returns the user identity.
func (p *OIDCProvider) VerifyIDToken(ctx context.Context, idToken, expectedNonce string) (*OIDCIdentity, error) {
	var c oidcClaims
	keyfunc := func(t *jwt.Token) (any, error) {
		if _, ok := t.Method.(*jwt.SigningMethodRSA); !ok {
			return nil, fmt.Errorf("unexpected alg %v", t.Method.Alg())
		}
		kid, _ := t.Header["kid"].(string)
		return p.fetchKey(ctx, kid)
	}
	_, err := jwt.ParseWithClaims(idToken, &c, keyfunc,
		jwt.WithIssuer(p.Issuer), jwt.WithAudience(p.ClientID), jwt.WithExpirationRequired())
	if err != nil {
		return nil, fmt.Errorf("id_token: %w", err)
	}
	if expectedNonce != "" && c.Nonce != expectedNonce {
		return nil, fmt.Errorf("id_token: nonce mismatch")
	}
	if c.Email == "" {
		return nil, fmt.Errorf("id_token: no email claim")
	}
	return &OIDCIdentity{Email: c.Email, Name: c.Name}, nil
}

type jwksDoc struct {
	Keys []struct {
		Kty, Kid, N, E string
	} `json:"keys"`
}

func (p *OIDCProvider) fetchKey(ctx context.Context, kid string) (*rsa.PublicKey, error) {
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, p.jwksURL, nil)
	resp, err := p.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("jwks: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("jwks: status %d", resp.StatusCode)
	}
	var doc jwksDoc
	if err := json.NewDecoder(resp.Body).Decode(&doc); err != nil {
		return nil, fmt.Errorf("jwks: %w", err)
	}
	for _, k := range doc.Keys {
		if k.Kty != "RSA" || k.Kid != kid {
			continue
		}
		n, err1 := base64.RawURLEncoding.DecodeString(k.N)
		e, err2 := base64.RawURLEncoding.DecodeString(k.E)
		if err1 != nil || err2 != nil {
			continue
		}
		exp := 0
		for _, b := range e {
			exp = exp<<8 | int(b)
		}
		return &rsa.PublicKey{N: new(big.Int).SetBytes(n), E: exp}, nil
	}
	return nil, fmt.Errorf("jwks: no RSA key for kid %q", kid)
}
