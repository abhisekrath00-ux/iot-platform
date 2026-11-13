package setup

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"github.com/abhisekrath00-ux/iot-platform/edge/internal/claim"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type MigrationRequest struct {
	MigrationCSR
	ID        string    `json:"id"`
	State     string    `json:"state"`
	ExpiresAt time.Time `json:"expires_at"`
}
type MigrationBundle struct {
	RequestID  string `json:"-"`
	State      string `json:"state"`
	KeySHA256  string `json:"key_sha256"`
	CSRSHA256  string `json:"csr_sha256"`
	ClientCert string `json:"client_cert_pem"`
	DeviceCA   string `json:"device_ca_pem"`
	BrokerCA   string `json:"broker_ca_pem"`
	Host       string `json:"broker_host"`
	Port       int    `json:"broker_port"`
}

func migrationHTTP(ctx context.Context, client *http.Client, server string, id *claim.Identity, method, path string, body any, out any) error {
	u, e := url.Parse(server)
	if e != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
		return fmt.Errorf("use the verified server HTTPS address only")
	}
	ip := net.ParseIP(u.Hostname())
	if u.Scheme != "https" && !(u.Scheme == "http" && (u.Hostname() == "localhost" || (ip != nil && ip.IsLoopback()))) {
		return fmt.Errorf("HTTPS required")
	}
	if id == nil || id.GatewayID == "" || id.IngestToken == "" {
		return fmt.Errorf("saved gateway identity required; do not claim again")
	}
	var b []byte
	if body != nil {
		b, e = json.Marshal(body)
		if e != nil {
			return e
		}
	}
	req, e := http.NewRequestWithContext(ctx, method, strings.TrimRight(u.String(), "/")+path, bytes.NewReader(b))
	if e != nil {
		return e
	}
	req.Header.Set("Authorization", "Bearer "+id.IngestToken)
	req.Header.Set("Content-Type", "application/json")
	c := *client
	c.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	resp, e := c.Do(req)
	if e != nil {
		return fmt.Errorf("secure server request failed: %w", e)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 && resp.StatusCode != 201 {
		return fmt.Errorf("secure pairing request returned HTTP %d; identity unchanged", resp.StatusCode)
	}
	raw, e := io.ReadAll(io.LimitReader(resp.Body, 131073))
	if e != nil || len(raw) > 131072 {
		return fmt.Errorf("invalid pairing response size")
	}
	if json.Unmarshal(raw, out) != nil {
		return fmt.Errorf("invalid pairing response")
	}
	return nil
}

// PrepareMigration persists public CSR before sending, so a lost response can be
// retried without issuing a different identity request. No saved key is changed.
func PrepareMigration(ctx context.Context, client *http.Client, server, dir, serial string, id *claim.Identity) (MigrationRequest, error) {
	var saved MigrationRequest
	unlock, e := migrationLock(dir)
	if e != nil {
		return saved, e
	}
	defer unlock()
	path := filepath.Join(dir, "migration-request.json")
	raw, e := os.ReadFile(path)
	if e == nil {
		if json.Unmarshal(raw, &saved) != nil {
			return saved, fmt.Errorf("saved pairing request unreadable; recover it before retrying")
		}
		current, e := ExistingKeyCSR(dir, serial)
		if e != nil {
			return saved, e
		}
		if current.KeySHA256 != saved.KeySHA256 {
			return saved, fmt.Errorf("saved key changed; pairing request cannot be reused")
		}
		if !saved.ExpiresAt.IsZero() && !time.Now().Before(saved.ExpiresAt) {
			archive := path + ".expired-" + fmt.Sprint(time.Now().UnixNano())
			if e = os.Rename(path, archive); e != nil {
				return saved, fmt.Errorf("expired public request could not be archived; identity unchanged")
			}
			return saved, fmt.Errorf("expired public request archived; press Request secure pairing again to make a fresh request with the same saved key")
		}
	} else if !os.IsNotExist(e) {
		return saved, e
	} else {
		csr, e := ExistingKeyCSR(dir, serial)
		if e != nil {
			return saved, e
		}
		saved.MigrationCSR = csr
		b, _ := json.Marshal(saved)
		f, e := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if e != nil {
			return saved, e
		}
		_, e = f.Write(b)
		if e == nil {
			e = f.Sync()
		}
		ce := f.Close()
		if e != nil {
			return saved, e
		}
		if ce != nil {
			return saved, ce
		}
	}
	var out MigrationRequest
	if e = migrationHTTP(ctx, client, server, id, "POST", "/v1/edge/self/identity-migrations", map[string]string{"csr_pem": saved.CSRPEM}, &out); e != nil {
		return saved, e
	}
	if out.ID == "" || out.KeySHA256 != saved.KeySHA256 || out.CSRSHA256 != saved.CSRSHA256 {
		return saved, fmt.Errorf("server pairing identity mismatch")
	}
	out.CSRPEM = saved.CSRPEM
	b, _ := json.Marshal(out)
	tmp := path + ".next"
	if e = os.WriteFile(tmp, b, 0600); e != nil {
		return saved, e
	}
	if e = os.Rename(tmp, path); e != nil {
		return saved, e
	}
	return out, nil
}
func FetchMigration(ctx context.Context, client *http.Client, server, dir, serial string, id *claim.Identity) (MigrationBundle, error) {
	var b MigrationBundle
	raw, e := os.ReadFile(filepath.Join(dir, "migration-request.json"))
	if e != nil {
		return b, fmt.Errorf("request secure pairing first")
	}
	var req MigrationRequest
	if json.Unmarshal(raw, &req) != nil || req.ID == "" {
		return b, fmt.Errorf("saved pairing request incomplete; retry Request pairing")
	}
	if strings.ContainsAny(req.ID, "/?#\\") {
		return b, fmt.Errorf("invalid saved request ID")
	}
	if e = migrationHTTP(ctx, client, server, id, "GET", "/v1/edge/self/identity-migrations/"+req.ID, nil, &b); e != nil {
		return b, e
	}
	if b.State != "issued" {
		return b, fmt.Errorf("waiting for workspace administrator fingerprint approval")
	}
	if b.KeySHA256 != req.KeySHA256 || b.CSRSHA256 != req.CSRSHA256 {
		return b, fmt.Errorf("approved request identity mismatch")
	}
	current, e := ExistingKeyCSR(dir, serial)
	if e != nil {
		return b, e
	}
	if current.KeySHA256 != b.KeySHA256 {
		return b, fmt.Errorf("saved private key changed")
	}
	b.RequestID = req.ID
	return b, nil
}
func ValidateMigrationBundle(dir, serial string, b MigrationBundle) (*tls.Config, error) {
	key, e := os.ReadFile(filepath.Join(dir, "identity.key"))
	if e != nil {
		return nil, e
	}
	pair, e := tls.X509KeyPair([]byte(b.ClientCert), key)
	if e != nil {
		return nil, fmt.Errorf("issued certificate does not match saved key")
	}
	leaf, e := x509.ParseCertificate(pair.Certificate[0])
	if e != nil || leaf.Subject.CommonName != serial {
		return nil, fmt.Errorf("issued certificate serial mismatch")
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM([]byte(b.DeviceCA)) {
		return nil, fmt.Errorf("invalid device issuing CA")
	}
	inter := x509.NewCertPool()
	for _, raw := range pair.Certificate[1:] {
		c, e := x509.ParseCertificate(raw)
		if e != nil {
			return nil, e
		}
		inter.AddCert(c)
	}
	if _, e = leaf.Verify(x509.VerifyOptions{Roots: roots, Intermediates: inter, KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}}); e != nil {
		return nil, fmt.Errorf("issued certificate validation failed")
	}
	broker := x509.NewCertPool()
	if !broker.AppendCertsFromPEM([]byte(b.BrokerCA)) {
		return nil, fmt.Errorf("invalid broker trust bundle")
	}
	if b.Host == "" || strings.ContainsAny(b.Host, "/@?#\\ \r\n\t") || b.Port < 1 || b.Port > 65535 {
		return nil, fmt.Errorf("invalid secure broker address")
	}
	ip := net.ParseIP(b.Host)
	if strings.EqualFold(b.Host, "localhost") || (ip != nil && (ip.IsLoopback() || ip.IsUnspecified())) {
		return nil, fmt.Errorf("secure broker must be reachable from this gateway")
	}
	return &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: broker, Certificates: []tls.Certificate{pair}, ServerName: b.Host}, nil
}

// ReportSerialPorts sends only explicit OS inventory, never a serial probe.
func ReportSerialPorts(ctx context.Context, client *http.Client, server string, id *claim.Identity, ports []string) error {
	var out map[string]any
	return migrationHTTP(ctx, client, server, id, "POST", "/v1/edge/self/serial-ports", map[string]any{"ports": ports}, &out)
}
