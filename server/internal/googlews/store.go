package googlews

import (
	"context"
	"errors"
	"strings"
)

// RefreshSecretName is the tenant-scoped secrets-store key holding the Google
// OAuth refresh token. It is write-only through the API and AES-256-GCM
// encrypted at rest like every other tenant secret.
const RefreshSecretName = "google_refresh_token"

// ScopesSecretName holds the scope string Google returned with the consent
// grant - the ground truth for which surfaces the tenant actually approved.
const ScopesSecretName = "google_granted_scopes"

// SecretStore is the slice of server/internal/secrets this package needs; the
// production wiring passes *secrets.Store, tests pass a fake.
type SecretStore interface {
	Put(ctx context.Context, tenant, name, by string, value []byte) error
	Get(ctx context.Context, tenant, name string) ([]byte, error)
	Delete(ctx context.Context, tenant, name string) (bool, error)
}

var ErrNotConnected = errors.New("google workspace not connected for this tenant")

// SaveRefreshToken persists the refresh token from a consent exchange. Google
// only sends a refresh token on a consent grant - a plain refresh response
// carries none, so an empty token NEVER overwrites the stored one.
func SaveRefreshToken(ctx context.Context, s SecretStore, tenant, by string, t *Tokens) error {
	if t == nil || strings.TrimSpace(t.RefreshToken) == "" {
		return errors.New("googlews: no refresh token in the grant to save")
	}
	return s.Put(ctx, tenant, RefreshSecretName, by, []byte(t.RefreshToken))
}

// LoadRefreshToken returns the tenant's stored refresh token.
func LoadRefreshToken(ctx context.Context, s SecretStore, tenant string) (string, error) {
	b, err := s.Get(ctx, tenant, RefreshSecretName)
	if err != nil {
		return "", ErrNotConnected
	}
	return string(b), nil
}

// SaveGrantedScopes records the scope string from the consent exchange. An
// empty string means Google did not echo scopes; that still overwrites so a
// re-consent with fewer grants cannot leave wider stale scopes on record.
func SaveGrantedScopes(ctx context.Context, s SecretStore, tenant, by, scope string) error {
	return s.Put(ctx, tenant, ScopesSecretName, by, []byte(scope))
}

// LoadGrantedScopes returns the recorded granted scope string; the boolean
// is false when no consent ever recorded one (older connections).
func LoadGrantedScopes(ctx context.Context, s SecretStore, tenant string) (string, bool) {
	b, err := s.Get(ctx, tenant, ScopesSecretName)
	if err != nil {
		return "", false
	}
	return string(b), true
}

// ScopeGranted reports whether the recorded grant covers one OAuth scope.
func ScopeGranted(recorded, scope string) bool {
	for _, f := range strings.Fields(recorded) {
		if f == scope {
			return true
		}
	}
	return false
}

// Disconnect forgets the tenant's Google grant. Future surface calls then
// fail with ErrNotConnected until the admin re-consents.
func Disconnect(ctx context.Context, s SecretStore, tenant string) error {
	_, err := s.Delete(ctx, tenant, RefreshSecretName)
	_, _ = s.Delete(ctx, tenant, ScopesSecretName)
	return err
}

// AccessToken loads the tenant's refresh token and mints a fresh access token
// for one API call batch. The access token is returned to the caller and
// never persisted anywhere.
func AccessToken(ctx context.Context, p *Provider, s SecretStore, tenant string) (string, error) {
	rt, err := LoadRefreshToken(ctx, s, tenant)
	if err != nil {
		return "", err
	}
	t, err := p.Refresh(ctx, rt)
	if err != nil {
		return "", err
	}
	return t.AccessToken, nil
}
