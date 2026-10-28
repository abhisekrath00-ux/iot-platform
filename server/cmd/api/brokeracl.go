package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"sync"

	"github.com/abhisekrath00-ux/iot-platform/server/internal/brokeracl"
)

// regenerateBrokerACL rewrites the mosquitto acl_file from every active
// gateway that holds an issued certificate. Called after each successful
// claim and via POST /v1/broker/acl/regenerate (redeploy hook). No-op unless
// BROKER_ACL_FILE is set; an unconfigured call returns a distinct error.
// Mosquitto picks up the new file on SIGHUP; see docs/broker-acl.md.
var brokerACLWriteMu sync.Mutex
var errBrokerACLDisabled = errors.New("BROKER_ACL_FILE is not configured")

func (s *server) regenerateBrokerACL(ctx context.Context) (result error) {
	defer func() {
		if result != nil && !errors.Is(result, errBrokerACLDisabled) {
			log.Printf("brokeracl regeneration failed: %v", result)
		}
	}()
	brokerACLWriteMu.Lock()
	defer brokerACLWriteMu.Unlock()
	path := os.Getenv("BROKER_ACL_FILE")
	if path == "" {
		return errBrokerACLDisabled
	}
	rows, err := s.st.Pool.Query(ctx,
		`SELECT serial, tenant_id, id, kind='direct' FROM gateways
		 WHERE status='active' AND ((cert_fingerprint IS NOT NULL AND cert_fingerprint<>'')
		   OR (auth_mode='password' AND kind='direct' AND broker_pw_hash IS NOT NULL
		       AND EXISTS (SELECT 1 FROM tenant_direct_auth a WHERE a.tenant_id=gateways.tenant_id AND a.password_enabled)))`)
	if err != nil {
		return fmt.Errorf("ACL query: %w", err)
	}
	defer rows.Close()
	entries := []brokeracl.Entry{}
	for rows.Next() {
		var e brokeracl.Entry
		if err := rows.Scan(&e.Serial, &e.TenantID, &e.GatewayID, &e.Direct); err != nil {
			return fmt.Errorf("ACL row: %w", err)
		}
		entries = append(entries, e)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("ACL read: %w", err)
	}
	rows.Close()
	pw := os.Getenv("BROKER_DIRECT_PASSWD_FILE")
	var pe []brokeracl.PasswdEntry
	if pw != "" {
		prow, err := s.st.Pool.Query(ctx, `SELECT g.serial, g.broker_pw_hash FROM gateways g JOIN tenant_direct_auth a ON a.tenant_id=g.tenant_id AND a.password_enabled WHERE g.status='active' AND g.kind='direct' AND g.auth_mode='password' AND g.broker_pw_hash IS NOT NULL`)
		if err != nil {
			return fmt.Errorf("password query: %w", err)
		}
		defer prow.Close()
		for prow.Next() {
			var e brokeracl.PasswdEntry
			if err = prow.Scan(&e.Username, &e.Hash); err != nil {
				return fmt.Errorf("password row: %w", err)
			}
			pe = append(pe, e)
		}
		if err = prow.Err(); err != nil {
			return fmt.Errorf("password read: %w", err)
		}
		prow.Close()
	}
	if err := brokeracl.WriteAtomic(path, brokeracl.Generate(entries)); err != nil {
		return fmt.Errorf("ACL write: %w", err)
	}
	if pw != "" {
		if err := brokeracl.WriteAtomic(pw, brokeracl.GeneratePasswd(pe)); err != nil {
			return fmt.Errorf("password write after ACL replacement: %w", err)
		}
	}
	log.Printf("brokeracl: wrote %d gateway blocks to %s (reload broker with SIGHUP)", len(entries), path)
	return nil
}

// regenerateBrokerACLHandler is the operator-facing redeploy hook, e.g. after
// a gateway is decommissioned and its block must disappear.
func (s *server) regenerateBrokerACLHandler(w http.ResponseWriter, r *http.Request) {
	if !requireRole(w, r, "admin") {
		return
	}
	if err := s.regenerateBrokerACL(r.Context()); err != nil {
		if errors.Is(err, errBrokerACLDisabled) {
			http.Error(w, "broker ACL generation not configured", 409)
		} else {
			log.Printf("brokeracl regenerate: %v", err)
			http.Error(w, "broker file generation failed; files may be partially replaced; do not reload until checked", 500)
		}
		return
	}
	s.audit(r, "brokeracl.regenerate", "", nil)
	writeJSON(w, 200, map[string]any{"status": "files_written", "reload_required": true})
}
