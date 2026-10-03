package main

// Second factor for physical control. Sign-in is by the customer's identity provider (SSO), which
// owns its own MFA; the platform keeps no passwords. What this adds is a one-time code from an
// authenticator app at the moment someone approves a control command, when an admin turns on
// "require_totp_approval". An API key or the assistant can never approve anyway.

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/abhisekrath00-ux/iot-platform/server/internal/auth"
	"github.com/abhisekrath00-ux/iot-platform/server/internal/secrets"
	"github.com/abhisekrath00-ux/iot-platform/server/internal/totp"
)

const featureTOTP = "require_totp_approval"

func totpName(user string) string { return "totp:" + user }

func (s *server) totpSecret(r *http.Request) (secret []byte, confirmed bool, last int64, ok bool) {
	var blob []byte
	if s.st.Pool.QueryRow(r.Context(), `SELECT secret, confirmed, last_step FROM user_totp WHERE user_id=$1 AND tenant_id=$2`, auth.User(r), auth.Tenant(r)).Scan(&blob, &confirmed, &last) != nil {
		return nil, false, 0, false
	}
	if s.secrets == nil || len(s.secrets.Key) == 0 {
		return nil, false, 0, false
	}
	v, err := secrets.Open(s.secrets.Key, auth.Tenant(r), totpName(auth.User(r)), blob)
	if err != nil {
		return nil, false, 0, false
	}
	return v, confirmed, last, true
}

// verifyTOTP checks a code for the signed-in user and, if good, burns its time step.
func (s *server) verifyTOTP(r *http.Request, code string) bool {
	secret, confirmed, last, ok := s.totpSecret(r)
	if !ok || !confirmed {
		return false
	}
	step, good := totp.Verify(secret, code, time.Now(), last)
	if !good {
		return false
	}
	// claim the step atomically so two requests with one code cannot both pass
	tag, err := s.st.Pool.Exec(r.Context(), `UPDATE user_totp SET last_step=$3 WHERE user_id=$1 AND tenant_id=$2 AND last_step<$3`, auth.User(r), auth.Tenant(r), step)
	return err == nil && tag.RowsAffected() == 1
}

func (s *server) totpStatus(w http.ResponseWriter, r *http.Request) {
	_, confirmed, _, _ := s.totpSecret(r)
	writeJSON(w, 200, map[string]any{"enrolled": confirmed, "required_for_approval": s.featureEnabled(r, featureTOTP),
		"available": s.secrets != nil && len(s.secrets.Key) > 0})
}

// POST /v1/me/totp/begin: make a new secret (replacing an unconfirmed one). Not active until confirmed.
func (s *server) totpBegin(w http.ResponseWriter, r *http.Request) {
	if auth.ViaKey(r) {
		http.Error(w, "interactive session required", 403)
		return
	}
	st := s.secretStore(w)
	if st == nil {
		return
	}
	if _, confirmed, _, _ := s.totpSecret(r); confirmed {
		http.Error(w, "already enrolled: remove the current authenticator first", 409)
		return
	}
	secret := totp.NewSecret()
	blob, err := secrets.Seal(st.Key, auth.Tenant(r), totpName(auth.User(r)), secret)
	if err != nil {
		http.Error(w, "could not store the secret", 500)
		return
	}
	if _, err := s.st.Pool.Exec(r.Context(), `INSERT INTO user_totp(user_id,tenant_id,secret) VALUES($1,$2,$3)
		ON CONFLICT (user_id) DO UPDATE SET secret=EXCLUDED.secret, confirmed=false, last_step=0, created_at=now()`, auth.User(r), auth.Tenant(r), blob); err != nil {
		http.Error(w, "db", 500)
		return
	}
	var email string
	s.st.Pool.QueryRow(r.Context(), `SELECT email FROM users WHERE id=$1`, auth.User(r)).Scan(&email)
	writeJSON(w, 200, map[string]any{"secret": totp.Base32(secret), "uri": totp.URI("Hexmon IoT", email, secret),
		"next": "Add it to your authenticator app, then confirm with the 6-digit code it shows."})
}

type codeIn struct {
	Code string `json:"code"`
}

func (s *server) totpConfirm(w http.ResponseWriter, r *http.Request) {
	if auth.ViaKey(r) {
		http.Error(w, "interactive session required", 403)
		return
	}
	var in codeIn
	json.NewDecoder(http.MaxBytesReader(w, r.Body, 512)).Decode(&in)
	secret, confirmed, last, ok := s.totpSecret(r)
	if !ok || confirmed {
		http.Error(w, "nothing to confirm: start enrollment first", 409)
		return
	}
	step, good := totp.Verify(secret, in.Code, time.Now(), last)
	if !good {
		http.Error(w, "wrong code", 400)
		return
	}
	s.st.Pool.Exec(r.Context(), `UPDATE user_totp SET confirmed=true, last_step=$3 WHERE user_id=$1 AND tenant_id=$2`, auth.User(r), auth.Tenant(r), step)
	s.audit(r, "totp.enroll", auth.User(r), nil)
	writeJSON(w, 200, map[string]any{"enrolled": true})
}

// DELETE /v1/me/totp {code}: remove it; needs a current code so a hijacked session cannot strip the factor.
func (s *server) totpRemove(w http.ResponseWriter, r *http.Request) {
	if auth.ViaKey(r) {
		http.Error(w, "interactive session required", 403)
		return
	}
	var in codeIn
	json.NewDecoder(http.MaxBytesReader(w, r.Body, 512)).Decode(&in)
	if !s.verifyTOTP(r, in.Code) {
		http.Error(w, "a current code is required", 400)
		return
	}
	s.st.Pool.Exec(r.Context(), `DELETE FROM user_totp WHERE user_id=$1 AND tenant_id=$2`, auth.User(r), auth.Tenant(r))
	s.audit(r, "totp.remove", auth.User(r), nil)
	w.WriteHeader(204)
}
