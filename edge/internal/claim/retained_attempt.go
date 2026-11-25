package claim

// Experimental retained-attempt primitive. Not wired to CLI or installer.
// A pending attempt always refuses replay. Reconciliation needs a separate
// server-authenticated exact-attempt protocol, which is not implemented here.
import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// RetainedClaimOnce makes at most one POST, only after syncing the key and
// public intent in a newly created private directory. Any retained directory
// refuses all subsequent POSTs, even if it contains only partial preparation.
// The caller must provide a new leaf path under a trusted private parent.
// This primitive does not clear pending state, install identity, or start service.
func RetainedClaimOnce(ctx context.Context, client *http.Client, dir, server, code, serial string) (*Identity, error) {
	u, err := url.Parse(server)
	if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
		return nil, fmt.Errorf("verified server address required")
	}
	ip := net.ParseIP(u.Hostname())
	if u.Scheme != "https" && !(u.Scheme == "http" && (u.Hostname() == "localhost" || (ip != nil && ip.IsLoopback()))) {
		return nil, fmt.Errorf("HTTPS required")
	}
	if code == "" || serial == "" || client == nil {
		return nil, fmt.Errorf("claim inputs required")
	}
	// Mkdir is the exclusive attempt gate; never MkdirAll or reuse an existing leaf.
	if err = os.Mkdir(dir, 0700); err != nil {
		return nil, fmt.Errorf("attempt exists or cannot be created; preserve and reconcile, do not retry")
	}
	syncFile := func(name string, b []byte) error {
		f, e := os.OpenFile(filepath.Join(dir, name), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if e != nil {
			return e
		}
		_, e = f.Write(b)
		if e == nil {
			e = f.Sync()
		}
		ce := f.Close()
		if e != nil {
			return e
		}
		return ce
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	der, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return nil, err
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: der})
	if err = syncFile("identity.key", keyPEM); err != nil {
		return nil, err
	}
	csr, err := x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{Subject: pkix.Name{CommonName: serial}}, key)
	if err != nil {
		return nil, err
	}
	csrPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: csr})
	sum := sha256.Sum256(csr)
	intent, _ := json.Marshal(map[string]any{"version": 1, "server": strings.TrimRight(u.String(), "/"), "serial": serial, "csr_sha256": hex.EncodeToString(sum[:]), "csr_pem": string(csrPEM), "replay_allowed": false})
	if err = syncFile("intent.json", intent); err != nil {
		return nil, err
	}
	// A separate marker is synced before network. It never asserts server success.
	if err = syncFile("request-started", []byte("outcome uncertain until exact authenticated reconciliation\n")); err != nil {
		return nil, err
	}
	b, _ := json.Marshal(map[string]string{"claim_code": code, "serial": serial, "csr_pem": string(csrPEM)})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(u.String(), "/")+"/v1/enrollment/claim", strings.NewReader(string(b)))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	c := *client
	c.Timeout = 15 * time.Second
	c.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	resp, err := c.Do(req)
	if err != nil {
		return nil, fmt.Errorf("claim outcome uncertain; retained attempt must be reconciled")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("claim returned HTTP %d; attempt retained, no retry", resp.StatusCode)
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 131073))
	if err != nil || len(raw) > 131072 {
		return nil, fmt.Errorf("claim response invalid; attempt retained")
	}
	var id Identity
	if json.Unmarshal(raw, &id) != nil || id.GatewayID == "" || id.IngestToken == "" {
		return nil, fmt.Errorf("claim response malformed; attempt retained")
	}
	// Preserve the response for later exact verification, never accept it as a
	// certificate/key/config reconciliation or installation-complete receipt.
	if err = syncFile("response.json", raw); err != nil {
		return nil, fmt.Errorf("response could not be retained; attempt unresolved")
	}
	id.ClientKeyPEM = keyPEM
	return &id, nil
}
