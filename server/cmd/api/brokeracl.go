package main

import (
	"context"
	"log"
	"net/http"
	"os"

	"github.com/abhisekrath00-ux/iot-platform/server/internal/brokeracl"
)

// regenerateBrokerACL rewrites the mosquitto acl_file from every active
// gateway that holds an issued certificate. Called after each successful
// claim and via POST /v1/broker/acl/regenerate (redeploy hook). No-op unless
// BROKER_ACL_FILE is set - the lab broker (allow_anonymous) has no ACL file.
// Mosquitto picks up the new file on SIGHUP; see docs/broker-acl.md.
func (s *server) regenerateBrokerACL(ctx context.Context) {
	path := os.Getenv("BROKER_ACL_FILE")
	if path == "" {
		return
	}
	rows, err := s.st.Pool.Query(ctx,
		`SELECT serial, tenant_id, id, kind='direct' FROM gateways
		 WHERE status='active' AND cert_fingerprint IS NOT NULL AND cert_fingerprint<>''`)
	if err != nil {
		log.Printf("brokeracl: query: %v", err)
		return
	}
	defer rows.Close()
	entries := []brokeracl.Entry{}
	for rows.Next() {
		var e brokeracl.Entry
		if err := rows.Scan(&e.Serial, &e.TenantID, &e.GatewayID, &e.Direct); err == nil {
			entries = append(entries, e)
		}
	}
	if err := brokeracl.WriteAtomic(path, brokeracl.Generate(entries)); err != nil {
		log.Printf("brokeracl: write %s: %v", path, err)
		return
	}
	log.Printf("brokeracl: wrote %d gateway blocks to %s (reload broker with SIGHUP)", len(entries), path)
}

// regenerateBrokerACLHandler is the operator-facing redeploy hook, e.g. after
// a gateway is decommissioned and its block must disappear.
func (s *server) regenerateBrokerACLHandler(w http.ResponseWriter, r *http.Request) {
	if !requireRole(w, r, "admin") {
		return
	}
	s.regenerateBrokerACL(r.Context())
	s.audit(r, "brokeracl.regenerate", "", nil)
	writeJSON(w, 200, map[string]any{"status": "regenerated"})
}
