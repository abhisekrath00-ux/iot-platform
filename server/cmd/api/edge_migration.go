package main

import (
	"crypto/ecdsa"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"github.com/abhisekrath00-ux/iot-platform/server/internal/auth"
	"github.com/abhisekrath00-ux/iot-platform/server/internal/pki"
	"github.com/google/uuid"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
)

func migrationCSR(raw, serial string) (string, string, error) {
	if len(raw) > 16384 {
		return "", "", fmt.Errorf("CSR too large")
	}
	b, rest := pem.Decode([]byte(raw))
	if b == nil || b.Type != "CERTIFICATE REQUEST" || strings.TrimSpace(string(rest)) != "" {
		return "", "", fmt.Errorf("one CSR PEM required")
	}
	c, e := x509.ParseCertificateRequest(b.Bytes)
	if e != nil {
		return "", "", fmt.Errorf("invalid CSR")
	}
	if e = c.CheckSignature(); e != nil {
		return "", "", fmt.Errorf("CSR signature invalid")
	}
	if c.Subject.CommonName != serial || len(c.DNSNames) > 0 || len(c.IPAddresses) > 0 || len(c.EmailAddresses) > 0 || len(c.URIs) > 0 {
		return "", "", fmt.Errorf("CSR must name only this gateway serial")
	}
	switch k := c.PublicKey.(type) {
	case *ecdsa.PublicKey:
		if k.Curve.Params().BitSize < 256 {
			return "", "", fmt.Errorf("weak EC key")
		}
	case *rsa.PublicKey:
		if k.N.BitLen() < 2048 {
			return "", "", fmt.Errorf("weak RSA key")
		}
	default:
		return "", "", fmt.Errorf("unsupported public key")
	}
	pub, e := x509.MarshalPKIXPublicKey(c.PublicKey)
	if e != nil {
		return "", "", e
	}
	a, h := sha256.Sum256(b.Bytes), sha256.Sum256(pub)
	return hex.EncodeToString(a[:]), hex.EncodeToString(h[:]), nil
}

// Trust forwarding only from an explicitly configured proxy socket, never an
// arbitrary X-Forwarded-Proto supplied by a client.
func migrationTransport(w http.ResponseWriter, r *http.Request) bool {
	host, _, _ := net.SplitHostPort(r.RemoteAddr)
	ip := net.ParseIP(host)
	proxy := net.ParseIP(os.Getenv("EDGE_HTTPS_PROXY_IP"))
	if r.TLS != nil || (ip != nil && ip.IsLoopback()) || (ip != nil && proxy != nil && ip.Equal(proxy) && r.Header.Get("X-Forwarded-Proto") == "https") {
		return true
	}
	http.Error(w, "verified HTTPS transport is required for identity migration", 403)
	return false
}
func validateMigrationCA(cert, key []byte) error {
	pair, e := tls.X509KeyPair(cert, key)
	if e != nil {
		return e
	}
	c, e := x509.ParseCertificate(pair.Certificate[0])
	if e != nil {
		return e
	}
	if !c.IsCA || c.KeyUsage&x509.KeyUsageCertSign == 0 || time.Now().Before(c.NotBefore) || !time.Now().Before(c.NotAfter) {
		return fmt.Errorf("CA is invalid or expired")
	}
	return nil
}
func (s *server) migrationGateway(w http.ResponseWriter, r *http.Request) (string, bool) {
	if !migrationTransport(w, r) {
		return "", false
	}
	if !requireRole(w, r, "gateway") {
		return "", false
	}
	if auth.ViaKey(r) {
		http.Error(w, "saved gateway JWT required", 403)
		return "", false
	}
	var serial, status, fp string
	e := s.st.Pool.QueryRow(r.Context(), `SELECT serial,status,COALESCE(cert_fingerprint,'') FROM gateways WHERE id=$1 AND tenant_id=$2`, auth.User(r), auth.Tenant(r)).Scan(&serial, &status, &fp)
	if e != nil {
		http.Error(w, "gateway not found", 404)
		return "", false
	}
	if status != "active" {
		http.Error(w, "gateway is not active", 403)
		return "", false
	}
	return serial, true
}
func migrationAdmin(w http.ResponseWriter, r *http.Request) bool {
	if !migrationTransport(w, r) {
		return false
	}
	if !requireRole(w, r, "admin") {
		return false
	}
	if auth.ViaKey(r) || scopeOf(r) != "" {
		http.Error(w, "interactive workspace administrator required", 403)
		return false
	}
	return true
}
func (s *server) requestIdentityMigration(w http.ResponseWriter, r *http.Request) {
	serial, ok := s.migrationGateway(w, r)
	if !ok {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 20000)
	var in struct {
		CSR string `json:"csr_pem"`
	}
	if json.NewDecoder(r.Body).Decode(&in) != nil {
		http.Error(w, "invalid request", 400)
		return
	}
	csrhash, keyhash, e := migrationCSR(in.CSR, serial)
	if e != nil {
		http.Error(w, e.Error(), 400)
		return
	}
	tx, e := s.st.Pool.Begin(r.Context())
	if e != nil {
		dbErr(w, e)
		return
	}
	defer tx.Rollback(r.Context())
	var fp string
	if e = tx.QueryRow(r.Context(), `SELECT COALESCE(cert_fingerprint,'') FROM gateways WHERE id=$1 AND tenant_id=$2 AND status='active' AND serial=$3 FOR UPDATE`, auth.User(r), auth.Tenant(r), serial).Scan(&fp); e != nil {
		http.Error(w, "active gateway not found", 404)
		return
	}
	var id, state string
	var expires time.Time
	e = tx.QueryRow(r.Context(), `SELECT id,state,expires_at FROM edge_identity_migrations WHERE gateway_id=$1 AND csr_sha256=$2`, auth.User(r), csrhash).Scan(&id, &state, &expires)
	if e == nil {
		if state == "revoked" || !time.Now().Before(expires) {
			http.Error(w, "request expired or revoked; create a new request", 409)
			return
		}
		writeJSON(w, 200, map[string]any{"id": id, "state": state, "key_sha256": keyhash, "csr_sha256": csrhash, "expires_at": expires})
		return
	}
	if fp != "" {
		http.Error(w, "gateway already has a certificate; use recovery, not migration", 409)
		return
	}
	var n int
	if e = tx.QueryRow(r.Context(), `SELECT count(*) FROM edge_identity_migrations WHERE gateway_id=$1 AND created_at>now()-interval '1 hour'`, auth.User(r)).Scan(&n); e != nil {
		dbErr(w, e)
		return
	}
	if n >= 3 {
		http.Error(w, "too many migration requests; wait before retrying", 429)
		return
	}
	id = uuid.NewString()
	e = tx.QueryRow(r.Context(), `INSERT INTO edge_identity_migrations(id,tenant_id,gateway_id,serial,csr_pem,csr_sha256,key_sha256) VALUES($1,$2,$3,$4,$5,$6,$7) RETURNING expires_at`, id, auth.Tenant(r), auth.User(r), serial, in.CSR, csrhash, keyhash).Scan(&expires)
	if e != nil {
		dbErr(w, e)
		return
	}
	if e = tx.Commit(r.Context()); e != nil {
		dbErr(w, e)
		return
	}
	s.audit(r, "edge.migration.request", id, map[string]any{"key_sha256": keyhash, "csr_sha256": csrhash})
	writeJSON(w, 201, map[string]any{"id": id, "state": "pending", "key_sha256": keyhash, "csr_sha256": csrhash, "expires_at": expires})
}
func (s *server) listIdentityMigrations(w http.ResponseWriter, r *http.Request) {
	if !migrationAdmin(w, r) {
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	rows, e := s.st.Pool.Query(r.Context(), `SELECT id,gateway_id,serial,key_sha256,csr_sha256,state,created_at,expires_at FROM edge_identity_migrations WHERE tenant_id=$1 AND expires_at>now() ORDER BY created_at DESC LIMIT 100`, auth.Tenant(r))
	if e != nil {
		dbErr(w, e)
		return
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var id, gw, serial, key, csr, state string
		var created, expires time.Time
		if e = rows.Scan(&id, &gw, &serial, &key, &csr, &state, &created, &expires); e != nil {
			dbErr(w, e)
			return
		}
		out = append(out, map[string]any{"id": id, "gateway_id": gw, "serial": serial, "key_sha256": key, "csr_sha256": csr, "state": state, "created_at": created, "expires_at": expires})
	}
	if rows.Err() != nil {
		dbErr(w, rows.Err())
		return
	}
	writeJSON(w, 200, out)
}
func (s *server) approveIdentityMigration(w http.ResponseWriter, r *http.Request) {
	if !migrationAdmin(w, r) {
		return
	}
	var in struct {
		Key      string `json:"key_sha256"`
		CSR      string `json:"csr_sha256"`
		Compared bool   `json:"compared_local_fingerprint"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, 2048)
	if json.NewDecoder(r.Body).Decode(&in) != nil || !in.Compared {
		http.Error(w, "compare the local public-key fingerprint before approval", 400)
		return
	}
	// No generation fallback. Deployment must already have trusted TLS and device CA.
	host := strings.TrimSpace(os.Getenv("EDGE_MQTT_HOST"))
	ip := net.ParseIP(host)
	port, pe := strconv.Atoi(os.Getenv("EDGE_MQTT_PORT"))
	if os.Getenv("EDGE_MQTT_TLS") != "true" || host == "" || !hostRe.MatchString(host) || strings.EqualFold(host, "localhost") || (ip != nil && (ip.IsLoopback() || ip.IsUnspecified())) || pe != nil || port < 1 || port > 65535 {
		http.Error(w, "secure broker endpoint is not configured", 409)
		return
	}
	ca, e := os.ReadFile(os.Getenv("MTLS_CA_CERT"))
	if e != nil {
		http.Error(w, "configured device CA unavailable", 409)
		return
	}
	key, e := os.ReadFile(os.Getenv("MTLS_CA_KEY"))
	if e != nil {
		http.Error(w, "configured device CA unavailable", 409)
		return
	}
	if e = validateMigrationCA(ca, key); e != nil {
		http.Error(w, "configured device CA invalid or expired", 409)
		return
	}
	broker, e := os.ReadFile(os.Getenv("EDGE_MQTT_CA_FILE"))
	if e != nil {
		http.Error(w, "broker trust bundle unavailable", 409)
		return
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(broker) {
		http.Error(w, "broker trust bundle invalid", 409)
		return
	}
	tx, e := s.st.Pool.Begin(r.Context())
	if e != nil {
		dbErr(w, e)
		return
	}
	defer tx.Rollback(r.Context())
	var gw, serial, csr, keyhash, csrhash, state, existing string
	var expires time.Time
	e = tx.QueryRow(r.Context(), `SELECT m.gateway_id,m.serial,m.csr_pem,m.key_sha256,m.csr_sha256,m.state,m.expires_at,COALESCE(g.cert_fingerprint,'') FROM edge_identity_migrations m JOIN gateways g ON g.id=m.gateway_id AND g.tenant_id=m.tenant_id WHERE m.id=$1 AND m.tenant_id=$2 AND g.status='active' AND g.serial=m.serial FOR UPDATE OF m,g`, r.PathValue("id"), auth.Tenant(r)).Scan(&gw, &serial, &csr, &keyhash, &csrhash, &state, &expires, &existing)
	if e != nil {
		http.Error(w, "request not found", 404)
		return
	}
	if in.Key != keyhash || in.CSR != csrhash {
		http.Error(w, "fingerprint changed; compare again", 409)
		return
	}
	if !time.Now().Before(expires) || state == "revoked" {
		http.Error(w, "request expired or revoked", 409)
		return
	}
	if state == "issued" {
		s.writeIssuedPairing(w, r)
		return
	}
	if existing != "" {
		http.Error(w, "gateway identity changed; use recovery", 409)
		return
	}
	if _, _, e = migrationCSR(csr, serial); e != nil {
		http.Error(w, "stored CSR invalid", 409)
		return
	}
	cab, _ := pem.Decode(ca)
	cac, _ := x509.ParseCertificate(cab.Bytes)
	days := int(time.Until(cac.NotAfter).Hours() / 24)
	if days > 365 {
		days = 365
	}
	if days < 1 {
		http.Error(w, "device CA expires too soon", 409)
		return
	}
	cert, fp, e := pki.SignCSR(ca, key, []byte(csr), serial, days)
	if e != nil {
		http.Error(w, "configured CA cannot sign this request", 409)
		return
	}
	if _, e = tx.Exec(r.Context(), `UPDATE gateways SET cert_fingerprint=$1 WHERE id=$2 AND tenant_id=$3`, fp, gw, auth.Tenant(r)); e == nil {
		_, e = tx.Exec(r.Context(), `UPDATE edge_identity_migrations SET state='issued',approved_by=$1,cert_pem=$2,cert_fingerprint=$3 WHERE id=$4`, auth.User(r), string(cert), fp, r.PathValue("id"))
	}
	if e != nil {
		dbErr(w, e)
		return
	}
	if e = tx.Commit(r.Context()); e != nil {
		dbErr(w, e)
		return
	}
	s.audit(r, "edge.migration.approve", r.PathValue("id"), map[string]any{"gateway_id": gw, "key_sha256": keyhash, "cert_fingerprint": fp})
	s.writeIssuedPairing(w, r)
}
func (s *server) getIdentityMigration(w http.ResponseWriter, r *http.Request) {
	_, ok := s.migrationGateway(w, r)
	if !ok {
		return
	}
	var state, keyhash, csrhash, cert string
	var expires time.Time
	e := s.st.Pool.QueryRow(r.Context(), `SELECT m.state,m.key_sha256,m.csr_sha256,COALESCE(m.cert_pem,''),m.expires_at FROM edge_identity_migrations m JOIN gateways g ON g.id=m.gateway_id AND g.tenant_id=m.tenant_id WHERE m.id=$1 AND m.tenant_id=$2 AND m.gateway_id=$3 AND (m.state<>'issued' OR m.cert_fingerprint=g.cert_fingerprint)`, r.PathValue("id"), auth.Tenant(r), auth.User(r)).Scan(&state, &keyhash, &csrhash, &cert, &expires)
	if e != nil {
		http.Error(w, "request not found", 404)
		return
	}
	if state == "revoked" || !time.Now().Before(expires) {
		http.Error(w, "request expired or revoked", 409)
		return
	}
	out := map[string]any{"state": state, "key_sha256": keyhash, "csr_sha256": csrhash, "expires_at": expires}
	if state == "issued" {
		out["client_cert_pem"] = cert
		ca, e := os.ReadFile(os.Getenv("MTLS_CA_CERT"))
		if e != nil {
			http.Error(w, "device CA unavailable", 409)
			return
		}
		broker, e := os.ReadFile(os.Getenv("EDGE_MQTT_CA_FILE"))
		if e != nil {
			http.Error(w, "broker trust bundle unavailable", 409)
			return
		}
		out["device_ca_pem"] = string(ca)
		out["broker_ca_pem"] = string(broker)
		out["broker_host"] = os.Getenv("EDGE_MQTT_HOST")
		port, _ := strconv.Atoi(os.Getenv("EDGE_MQTT_PORT"))
		out["broker_port"] = port
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, 200, out)
}

func (s *server) revokePendingIdentityMigration(w http.ResponseWriter, r *http.Request) {
	if !migrationAdmin(w, r) {
		return
	}
	tag, e := s.st.Pool.Exec(r.Context(), `UPDATE edge_identity_migrations SET state='revoked' WHERE id=$1 AND tenant_id=$2 AND state='pending'`, r.PathValue("id"), auth.Tenant(r))
	if e != nil {
		dbErr(w, e)
		return
	}
	if tag.RowsAffected() != 1 {
		http.Error(w, "pending request not found; issued certificates need explicit certificate revocation", 409)
		return
	}
	s.audit(r, "edge.migration.revoke", r.PathValue("id"), nil)
	writeJSON(w, 200, map[string]any{"state": "revoked"})
}

// Issuance is never a broker observation. An optional private watcher can send
// HUP after a new ACL, but Finish still requires an independent nonce receipt.
func (s *server) writeIssuedPairing(w http.ResponseWriter, r *http.Request) {
	state := "manual_reload_required"
	if certificateOnlyPairing() {
		state = "files_written_waiting_for_broker_proof"
		if err := s.regenerateBrokerACL(r.Context()); err != nil {
			state = "acl_unavailable_retry_finish"
		}
	}
	writeJSON(w, 200, map[string]any{"state": "issued", "broker_acl_state": state, "note": "Certificate issued, not connected. Finish secure pairing must observe a fresh own-gateway broker proof before installing certificate files. Unconfigured deployments still need explicit ACL/reload setup."})
}
