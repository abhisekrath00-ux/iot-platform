package main

import (
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"os"
	"time"

	"github.com/abhisekrath00-ux/iot-platform/server/internal/auth"
	"github.com/abhisekrath00-ux/iot-platform/server/internal/pairingproof"
	"github.com/google/uuid"
)

func certificateOnlyPairing() bool {
	return os.Getenv("BROKER_CERT_ONLY") == "1" && os.Getenv("MQTT_TLS") == "true" && os.Getenv("BROKER_ACL_FILE") != ""
}

func (s *server) createPairingProof(w http.ResponseWriter, r *http.Request) {
	serial, ok := s.migrationGateway(w, r)
	if !ok {
		return
	}
	if !certificateOnlyPairing() {
		http.Error(w, "certificate-only broker proof is not configured", 409)
		return
	}
	// Finish is explicit and recoverable: retry ACL generation for an already
	// issued certificate, without signing another certificate or replacing keys.
	if err := s.regenerateBrokerACL(r.Context()); err != nil {
		http.Error(w, "certificate is issued but broker ACL could not be prepared; no pairing proof started", 409)
		return
	}
	nonce := make([]byte, 32)
	if _, err := rand.Read(nonce); err != nil {
		http.Error(w, "proof unavailable", 500)
		return
	}
	token := hex.EncodeToString(nonce)
	id := "pair-proof-" + uuid.NewString()
	var fp string
	var expiry time.Time
	err := s.st.Pool.QueryRow(r.Context(), `INSERT INTO edge_pairing_proofs(id,migration_id,tenant_id,gateway_id,cert_fingerprint,nonce_sha256)
 SELECT $1,m.id,m.tenant_id,m.gateway_id,m.cert_fingerprint,$2
 FROM edge_identity_migrations m JOIN gateways g ON g.id=m.gateway_id AND g.tenant_id=m.tenant_id
 WHERE m.id=$3 AND m.tenant_id=$4 AND m.gateway_id=$5 AND m.serial=$6
 AND m.state='issued' AND m.expires_at>now() AND g.status='active' AND g.cert_fingerprint=m.cert_fingerprint
 ON CONFLICT(gateway_id) DO UPDATE SET id=EXCLUDED.id,migration_id=EXCLUDED.migration_id,
 tenant_id=EXCLUDED.tenant_id,cert_fingerprint=EXCLUDED.cert_fingerprint,nonce_sha256=EXCLUDED.nonce_sha256,
 created_at=now(),expires_at=now()+interval '60 seconds',observed_at=NULL
 RETURNING cert_fingerprint,expires_at`, id, pairingproof.Hash(token), r.PathValue("id"), auth.Tenant(r), auth.User(r), serial).Scan(&fp, &expiry)
	if err != nil {
		http.Error(w, "current issued identity request not found", 404)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, 201, map[string]any{"id": id, "nonce": token, "topic": "t/" + auth.Tenant(r) + "/g/" + auth.User(r) + "/pairing/proof", "cert_fingerprint": fp, "expires_at": expiry, "state": "waiting_for_broker_observation"})
}

func (s *server) getPairingProof(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.migrationGateway(w, r); !ok {
		return
	}
	if !certificateOnlyPairing() {
		http.Error(w, "certificate-only broker proof is not configured", 409)
		return
	}
	var fp string
	var observed *time.Time
	var expiry time.Time
	err := s.st.Pool.QueryRow(r.Context(), `SELECT p.cert_fingerprint,p.observed_at,p.expires_at
 FROM edge_pairing_proofs p JOIN gateways g ON g.id=p.gateway_id AND g.tenant_id=p.tenant_id
 JOIN edge_identity_migrations m ON m.id=p.migration_id
 WHERE p.id=$1 AND p.tenant_id=$2 AND p.gateway_id=$3 AND p.expires_at>now()
 AND g.status='active' AND g.cert_fingerprint=p.cert_fingerprint AND m.state='issued'
 AND m.cert_fingerprint=g.cert_fingerprint AND m.serial=g.serial`, r.PathValue("proof"), auth.Tenant(r), auth.User(r)).Scan(&fp, &observed, &expiry)
	if err != nil {
		http.Error(w, "current pairing proof not found or expired", 404)
		return
	}
	state := "waiting_for_broker_observation"
	if observed != nil {
		state = "observed"
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, 200, map[string]any{"id": r.PathValue("proof"), "state": state, "cert_fingerprint": fp, "observed_at": observed, "expires_at": expiry})
}
