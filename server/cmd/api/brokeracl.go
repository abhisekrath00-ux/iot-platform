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
		 WHERE status='active' AND ((cert_fingerprint IS NOT NULL AND cert_fingerprint<>'')
		   OR (auth_mode='password' AND kind='direct' AND broker_pw_hash IS NOT NULL
		       AND EXISTS (SELECT 1 FROM tenant_direct_auth a WHERE a.tenant_id=gateways.tenant_id AND a.password_enabled)))`)
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
	if pw := os.Getenv("BROKER_DIRECT_PASSWD_FILE"); pw != "" {
		prow, err := s.st.Pool.Query(ctx, `SELECT g.serial, g.broker_pw_hash FROM gateways g
			JOIN tenant_direct_auth a ON a.tenant_id=g.tenant_id AND a.password_enabled
			WHERE g.status='active' AND g.kind='direct' AND g.auth_mode='password' AND g.broker_pw_hash IS NOT NULL`)
		if err != nil {
			log.Printf("brokeracl: passwd query: %v", err)
		} else {
			var pe []brokeracl.PasswdEntry
			for prow.Next() {
				var e brokeracl.PasswdEntry
				if prow.Scan(&e.Username, &e.Hash) == nil {
					pe = append(pe, e)
				}
			}
			prow.Close()
			if err := brokeracl.WriteAtomic(pw, brokeracl.GeneratePasswd(pe)); err != nil {
				log.Printf("brokeracl: write %s: %v", pw, err)
			}
		}
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
