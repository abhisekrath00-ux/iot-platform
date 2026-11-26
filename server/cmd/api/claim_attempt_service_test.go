package main

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/abhisekrath00-ux/iot-platform/server/internal/auth"
	"github.com/abhisekrath00-ux/iot-platform/server/internal/enroll"
	"github.com/abhisekrath00-ux/iot-platform/server/internal/pki"
	"github.com/golang-jwt/jwt/v5"
)

func TestCandidateProductRouterDefaultOff(t *testing.T) {
	mux := http.NewServeMux()
	close, e := registerCandidateEnrollment(t.Context(), mux, enroll.CandidateServiceConfig{})
	if e != nil {
		t.Fatal(e)
	}
	defer close()
	close()
	for _, path := range []string{"/candidate/claim", "/candidate/recover"} {
		r := httptest.NewRequest("POST", "https://example.invalid"+path, bytes.NewBufferString("{}"))
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		if w.Code != 404 {
			t.Fatal("disabled exposed endpoint", w.Code)
		}
	}
	// Enabled without a pool fails before installing a route, without panic.
	mux = http.NewServeMux()
	if _, e = registerCandidateEnrollment(t.Context(), mux, enroll.CandidateServiceConfig{Enabled: true}); e == nil {
		t.Fatal("missing configuration accepted")
	}
}

func TestIntegrationCandidateProductService(t *testing.T) {
	// A staged enrollment may retain its private attempt, but must never touch
	// pre-existing installed binary/config/identity or invoke service/user tools.
	installed := t.TempDir()
	sentinels := map[string][]byte{}
	for _, name := range []string{"bin/edge-agent", "etc/edge-agent.yaml", "state/identity.key", "state/identity.json", "unit/hexmon-edge.service"} {
		path := filepath.Join(installed, name)
		if e := os.MkdirAll(filepath.Dir(path), 0700); e != nil {
			t.Fatal(e)
		}
		sentinels[path] = []byte("existing installation preserved: " + name)
		if e := os.WriteFile(path, sentinels[path], 0600); e != nil {
			t.Fatal(e)
		}
	}
	commands := filepath.Join(installed, "forbidden-commands")
	if e := os.Mkdir(commands, 0700); e != nil {
		t.Fatal(e)
	}
	for _, name := range []string{"systemctl", "useradd", "usermod", "chown", "sudo", "curl"} {
		if e := os.WriteFile(filepath.Join(commands, name), []byte("#!/bin/sh\necho unexpected > \"$HEX_STAGING_FORBIDDEN_LOG\"\nexit 97\n"), 0700); e != nil {
			t.Fatal(e)
		}
	}
	t.Cleanup(func() {
		for path, expected := range sentinels {
			actual, e := os.ReadFile(path)
			if e != nil || !bytes.Equal(actual, expected) {
				t.Errorf("installed file changed: %s: %v", path, e)
			}
		}
		if _, e := os.Stat(filepath.Join(installed, "unexpected-command")); !os.IsNotExist(e) {
			t.Error("installer invoked forbidden host tool", e)
		}
		for _, name := range []string{"bin", "etc", "state", "unit"} {
			entries, e := os.ReadDir(filepath.Join(installed, name))
			want := 1
			if name == "state" {
				want = 2
			}
			if e != nil || len(entries) != want {
				t.Errorf("installed target pathset changed %s: %v", name, e)
			}
		}
	})
	// Optional local packaging acceptance runs the identical product/process-cut
	// scenarios through the real installer staging entrypoint, not a mock.
	productCommand := func(bin, flag string) *exec.Cmd {
		if script := os.Getenv("HEX_CLAIM_INSTALLER_SCRIPT"); script != "" {
			if bin == os.Getenv("HEX_CLAIM_FAULT_AGENT_BIN") {
				script = os.Getenv("HEX_CLAIM_FAULT_INSTALLER_SCRIPT")
				if script == "" {
					t.Fatal("fault installer required")
				}
			}
			flags := map[string]string{"-create-retained-claim": "--stage-retained-claim", "-recover-retained-claim": "--recover-staged-claim", "-review-retained-claim": "--review-staged-claim", "-review-staged-install": "--review-staged-install", "-create-staged-install": "--create-staged-install", "-prepare-private-install": "--prepare-private-install", "-accept-private-identity": "--accept-private-identity", "-review-runtime-mapping": "--review-runtime-mapping", "-assemble-private-runtime": "--assemble-private-runtime"}
			arg, ok := flags[flag]
			if !ok {
				t.Fatal("unexpected product action")
			}
			cmd := exec.Command("bash", script, arg)
			cmd.Env = append(os.Environ(), "HEXMON_PREFIX="+filepath.Join(installed, "bin"), "HEXMON_ETC="+filepath.Join(installed, "etc"), "HEXMON_STATE="+filepath.Join(installed, "state"), "HEXMON_UNIT_DIR="+filepath.Join(installed, "unit"), "HEX_STAGING_FORBIDDEN_LOG="+filepath.Join(installed, "unexpected-command"), "PATH="+commands+":"+os.Getenv("PATH"))
			return cmd
		}
		return exec.Command(bin, flag)
	}
	s, _ := testServer(t)
	// Exact fresh fixture ownership, registered before seedClaim's first write.
	seedCandidate := func(t *testing.T, s *server, tenant, serial string) (string, string) {
		return seedCandidateFixtureClaim(t, s, tenant, serial, seedClaim)
	}

	_, _ = s.st.Pool.Exec(t.Context(), `DROP TABLE IF EXISTS enrollment_attempt_candidate`)
	t.Cleanup(func() {
		_, _ = s.st.Pool.Exec(context.Background(), `DROP TABLE IF EXISTS enrollment_attempt_candidate`)
	})
	ca, cak, e := pki.GenerateCA("Synthetic service CA", 1)
	if e != nil {
		t.Fatal(e)
	}
	_, key, e := ed25519.GenerateKey(rand.Reader)
	if e != nil {
		t.Fatal(e)
	}
	cfg := enroll.CandidateServiceConfig{Enabled: true, Pool: s.st.Pool, Issuer: enroll.CandidateIssuer{CA: ca, CAKey: cak, ResponseKey: key, TokenKey: []byte("synthetic-service-hmac-key-not-owner-data"), Lifetime: 45 * time.Minute}}
	if service, e := enroll.NewCandidateService(t.Context(), cfg); e == nil {
		service.Close()
		t.Fatal("missing schema accepted")
	}
	var missing bool
	if e = s.st.Pool.QueryRow(t.Context(), `SELECT to_regclass('enrollment_attempt_candidate') IS NULL`).Scan(&missing); e != nil || !missing {
		t.Fatal("readiness created schema", e)
	}
	schema, e := os.ReadFile("../../internal/enroll/candidate.sql")
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.st.Pool.Exec(t.Context(), string(schema)); e != nil {
		t.Fatal(e)
	}
	// Constraint/type/nullability drift each refuse before route installation.
	for _, change := range []struct{ bad, undo string }{
		{`ALTER TABLE enrollment_attempt_candidate DROP CONSTRAINT enrollment_attempt_candidate_token_id_key`, `ALTER TABLE enrollment_attempt_candidate ADD UNIQUE(token_id)`},
		{`ALTER TABLE enrollment_attempt_candidate ALTER COLUMN response DROP NOT NULL`, `ALTER TABLE enrollment_attempt_candidate ALTER COLUMN response SET NOT NULL`},
		{`ALTER TABLE enrollment_attempt_candidate ALTER COLUMN expires_at TYPE timestamp`, `ALTER TABLE enrollment_attempt_candidate ALTER COLUMN expires_at TYPE timestamptz`},
	} {
		if _, e = s.st.Pool.Exec(t.Context(), change.bad); e != nil {
			t.Fatal(e)
		}
		if service, e := enroll.NewCandidateService(t.Context(), cfg); e == nil {
			service.Close()
			t.Fatal("schema drift accepted")
		}
		if _, e = s.st.Pool.Exec(t.Context(), change.undo); e != nil {
			t.Fatal(e)
		}
	}
	bad := cfg
	bad.Issuer.ResponseKey = append(ed25519.PrivateKey(nil), key...)
	bad.Issuer.ResponseKey[63] ^= 1
	if service, e := enroll.NewCandidateService(t.Context(), bad); e == nil {
		service.Close()
		t.Fatal("inconsistent key accepted")
	}
	codeBad, gwBad := seedCandidate(t, s, "itest-issued-refusal", "issued-refusal-serial")
	badDevice, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	badCSR, _ := x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{Subject: pkix.Name{CommonName: "issued-refusal-serial"}}, badDevice)
	badIn := enroll.AttemptInput{ID: hex.EncodeToString(make([]byte, 32)), Code: codeBad, Serial: "issued-refusal-serial", CSR: string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: badCSR}))}
	badIn.Signature, _ = ecdsa.SignASN1(rand.Reader, badDevice, enroll.AttemptProofDigest(badIn, false))
	for _, badMaterial := range []string{"fingerprint", "response"} {
		_, e = (enroll.CandidateAttempts{Pool: s.st.Pool}).Execute(t.Context(), badIn, false, func(b enroll.AttemptBinding) (enroll.CandidateIssued, error) {
			issued, e := cfg.Issuer.Issue(b)
			if e != nil {
				return enroll.CandidateIssued{}, e
			}
			if badMaterial == "fingerprint" {
				issued.Fingerprint = hex.EncodeToString(make([]byte, 32))
			} else {
				issued.Response = []byte(`{"malformed":true}`)
			}
			if e = cfg.Issuer.ValidateIssued(b, issued); e != nil {
				return enroll.CandidateIssued{}, e
			}
			return issued, nil
		})
		if e == nil || claimRedeemed(t, s, codeBad) {
			t.Fatal("bad issued material consumed claim", badMaterial, e)
		}
		var status string
		if e = s.st.Pool.QueryRow(t.Context(), `SELECT status FROM gateways WHERE id=$1`, gwBad).Scan(&status); e != nil || status != "pending" {
			t.Fatal("bad issued output activated gateway", e)
		}
		var results int
		if e = s.st.Pool.QueryRow(t.Context(), `SELECT count(*) FROM enrollment_attempt_candidate WHERE attempt_id=$1`, badIn.ID).Scan(&results); e != nil || results != 0 {
			t.Fatal("bad issued output retained result", e)
		}
	}
	t.Log("PASS actual PostgreSQL malformed signer response/fingerprint refusal leaves code unconsumed, gateway pending and no stored attempt")
	mux := http.NewServeMux()
	close, e := registerCandidateEnrollment(t.Context(), mux, cfg)
	if e != nil {
		t.Fatal(e)
	}
	defer close()
	// Construction snapshots authority; mutation by the caller must not corrupt it.
	cfg.Issuer.TokenKey[0] ^= 1
	cfg.Issuer.ResponseKey[63] ^= 1
	srv := httptest.NewTLSServer(mux)
	defer srv.Close()
	code, _ := seedCandidate(t, s, "itest-candidate-service", "service-serial")
	device, e := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if e != nil {
		t.Fatal(e)
	}
	csr, e := x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{Subject: pkix.Name{CommonName: "service-serial"}}, device)
	if e != nil {
		t.Fatal(e)
	}
	id := make([]byte, 32)
	_, _ = rand.Read(id)
	in := enroll.AttemptInput{ID: hex.EncodeToString(id), Code: code, Serial: "service-serial", CSR: string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: csr}))}
	post := func(recover bool) (int, []byte) {
		t.Helper()
		sig, e := ecdsa.SignASN1(rand.Reader, device, enroll.AttemptProofDigest(in, recover))
		if e != nil {
			t.Fatal(e)
		}
		body, _ := json.Marshal(map[string]any{"attempt_id": in.ID, "claim_code": in.Code, "serial": in.Serial, "csr_pem": in.CSR, "signature": sig})
		path := "/candidate/claim"
		if recover {
			path = "/candidate/recover"
		}
		resp, e := srv.Client().Post(srv.URL+path, "application/json", bytes.NewReader(body))
		if e != nil {
			t.Fatal(e)
		}
		defer resp.Body.Close()
		out, e := io.ReadAll(resp.Body)
		if e != nil {
			t.Fatal(e)
		}
		if resp.Header.Get("Cache-Control") != "no-store" {
			t.Fatal("cache enabled")
		}
		return resp.StatusCode, out
	}
	status, first := post(false)
	if status != 200 {
		t.Fatal("create", status, string(first))
	}
	status, next := post(true)
	if status != 200 || !bytes.Equal(first, next) {
		t.Fatal("exact recovery", status)
	}
	// Recovery and claim share one budget, including denied requests.
	if status, _ = post(true); status != 200 {
		t.Fatal(status)
	}
	if status, _ = post(false); status != 200 {
		t.Fatal(status)
	}
	if status, _ = post(true); status != 200 {
		t.Fatal(status)
	}
	if status, _ = post(false); status != 429 {
		t.Fatal("split endpoint rate bypass", status)
	}
	// Caller key mutation did not affect stored result and only one attempt exists.
	var count int
	if e = s.st.Pool.QueryRow(t.Context(), `SELECT count(*) FROM enrollment_attempt_candidate WHERE attempt_id=$1`, in.ID).Scan(&count); e != nil || count != 1 {
		t.Fatal(count, e)
	}
	t.Log("PASS actual product router helper: default off, no auto schema, readiness drift refusal, explicit synthetic HTTPS create/exact recover, shared per-IP limit, snapshotted trust; no startup flag or environment activation")
	// Exercise the assembled service through real socket loss and independent
	// compiled clients, not a fixture-built pair of handler registrations.
	cfg.Issuer.TokenKey[0] ^= 1
	cfg.Issuer.ResponseKey[63] ^= 1
	freshMux := http.NewServeMux()
	stop, e := registerCandidateEnrollment(t.Context(), freshMux, cfg)
	if e != nil {
		t.Fatal(e)
	}
	defer stop()
	var committed []byte
	var retainedDir, movedDir string
	var replaced atomic.Bool
	var lost atomic.Bool
	lossServer := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/candidate/claim" && !lost.Swap(true) {
			capture := httptest.NewRecorder()
			freshMux.ServeHTTP(capture, r)
			if capture.Code != 200 {
				t.Errorf("assembled create failed %d", capture.Code)
			}
			committed = append([]byte(nil), capture.Body.Bytes()...)
			conn, _, e := w.(http.Hijacker).Hijack()
			if e != nil {
				t.Error(e)
				return
			}
			_ = conn.Close()
			return
		}
		if r.URL.Path == "/candidate/recover" && !replaced.Swap(true) {
			// The child has opened its pinned store before sending the request.
			// Replace its complete pathname while the actual service is replying.
			if e := os.Rename(retainedDir, movedDir); e != nil {
				t.Error(e)
			}
			if e := os.Mkdir(retainedDir, 0700); e != nil {
				t.Error(e)
			}
		}
		freshMux.ServeHTTP(w, r)
	}))
	defer lossServer.Close()
	code2, gw2 := seedCandidate(t, s, "itest-service-process", "service-process-serial")
	parent := t.TempDir()
	if e = os.Chmod(parent, 0700); e != nil {
		t.Fatal(e)
	}
	dir := filepath.Join(parent, "attempt")
	retainedDir = dir
	movedDir = filepath.Join(parent, "retained-during-response")
	transport := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: lossServer.Certificate().Raw})
	invoke := func(recover bool) error {
		payload, _ := json.Marshal(map[string]any{"Dir": dir, "Server": lossServer.URL, "Code": code2, "Serial": "service-process-serial", "TransportCA": string(transport), "DeviceCA": string(ca), "ExpectedTenant": "itest-service-process", "ResponseKey": key.Public(), "Recover": recover})
		bin := os.Getenv("HEX_CLAIM_REVIEW_AGENT_BIN")
		if bin == "" {
			t.Fatal("actual product attempt binary required")
		}
		// Neither product command takes a fixture Recover switch.
		payload, _ = json.Marshal(map[string]any{"Dir": dir, "Server": lossServer.URL, "Code": code2, "Serial": "service-process-serial", "TransportCA": string(transport), "DeviceCA": string(ca), "ExpectedTenant": "itest-service-process", "ResponseKey": key.Public()})
		var cmd *exec.Cmd
		if recover {
			bin = os.Getenv("HEX_CLAIM_REVIEW_AGENT_BIN")
			if bin == "" {
				t.Fatal("actual product recovery binary required")
			}
			// The product adapter has no Recover/Create switch, only the fixed
			// recovery operation. Its input deliberately omits fixture flags.
			payload, _ = json.Marshal(map[string]any{"Dir": dir, "Server": lossServer.URL, "Code": code2, "Serial": "service-process-serial", "TransportCA": string(transport), "DeviceCA": string(ca), "ExpectedTenant": "itest-service-process", "ResponseKey": key.Public()})
			cmd = productCommand(bin, "-recover-retained-claim")
		} else {
			cmd = productCommand(bin, "-create-retained-claim")
		}
		cmd.Stdin = bytes.NewReader(payload)
		out, err := cmd.CombinedOutput()
		if recover && err == nil {
			var summary struct {
				State   string `json:"state"`
				Install bool   `json:"can_install"`
				Start   bool   `json:"can_start"`
				Clear   bool   `json:"can_clear_pending"`
			}
			if json.Unmarshal(out, &summary) != nil || summary.State != "verified_recovery_retained" || summary.Install || summary.Start || summary.Clear {
				t.Fatal("product recovery summary", string(out))
			}
			for _, secret := range []string{code2, "PRIVATE KEY", "ingest_token", "client_cert_pem", "csr_pem"} {
				if bytes.Contains(out, []byte(secret)) {
					t.Fatal("recovery output leaked sensitive material")
				}
			}
		}
		if err != nil {
			t.Log("child refusal", string(out))
		}
		return err
	}
	if e = invoke(false); e == nil {
		t.Fatal("missing loss refusal")
	}
	before, e := os.ReadFile(filepath.Join(dir, "identity.key"))
	if e != nil {
		t.Fatal(e)
	}
	if !claimRedeemed(t, s, code2) {
		t.Fatal("service did not commit before loss")
	}
	if e = invoke(false); e == nil {
		t.Fatal("product repeated create")
	}
	if e = invoke(true); e != nil {
		t.Fatal("service process recovery", e)
	}
	if replacement, e := os.ReadDir(dir); e != nil || len(replacement) != 0 {
		t.Fatal("recovery followed replacement path", e)
	}
	dir = movedDir
	after, e := os.ReadFile(filepath.Join(dir, "identity.key"))
	if e != nil || !bytes.Equal(before, after) {
		t.Fatal("key changed", e)
	}
	retained, e := os.ReadFile(filepath.Join(dir, "candidate-response.json"))
	if e != nil || !bytes.Equal(retained, committed) {
		t.Fatal("not exact assembled response", e)
	}
	var result struct {
		Gateway string `json:"gateway_id"`
	}
	if json.Unmarshal(retained, &result) != nil || result.Gateway != gw2 {
		t.Fatal("wrong gateway")
	}
	payload, _ := json.Marshal(map[string]any{"Dir": dir, "Server": lossServer.URL, "Serial": "service-process-serial", "DeviceCA": string(ca), "ExpectedTenant": "itest-service-process", "ResponseKey": key.Public()})
	agent := os.Getenv("HEX_CLAIM_REVIEW_AGENT_BIN")
	if agent == "" {
		t.Fatal("actual product review binary required")
	}
	cmd := productCommand(agent, "-review-retained-claim")
	cmd.Stdin = bytes.NewReader(payload)
	out, e := cmd.CombinedOutput()
	var review struct {
		State   string `json:"state"`
		Install bool   `json:"can_install"`
		Start   bool   `json:"can_start"`
		Clear   bool   `json:"can_clear_pending"`
	}
	if e != nil || json.Unmarshal(out, &review) != nil || review.State != "verified_saved_response" || review.Install || review.Start || review.Clear {
		t.Fatal("product review", e, string(out))
	}
	// A locally verified saved result must not short-circuit a subsequent
	// explicit recovery: consult current server state and refuse revocation.
	if e = invoke(true); e != nil {
		t.Fatal("exact repeat recovery", e)
	}
	if _, e = s.st.Pool.Exec(t.Context(), `UPDATE enrollment_attempt_candidate SET revoked=true WHERE gateway_id=$1`, gw2); e != nil {
		t.Fatal(e)
	}
	if e = invoke(true); e == nil {
		t.Fatal("product recovery ignored server revocation")
	}
	unchanged, e := os.ReadFile(filepath.Join(dir, "candidate-response.json"))
	if e != nil || !bytes.Equal(unchanged, retained) {
		t.Fatal("revocation refusal altered retained response", e)
	}
	unchanged, e = os.ReadFile(filepath.Join(dir, "identity.key"))
	if e != nil || !bytes.Equal(unchanged, before) {
		t.Fatal("revocation refusal altered retained key", e)
	}
	code3, gw3 := seedCandidate(t, s, "itest-product-normal-create", "normal-product-serial")
	newDir := filepath.Join(parent, "normal-attempt")
	newPayload, _ := json.Marshal(map[string]any{"Dir": newDir, "Server": lossServer.URL, "Code": code3, "Serial": "normal-product-serial", "TransportCA": string(transport), "DeviceCA": string(ca), "ExpectedTenant": "itest-product-normal-create", "ResponseKey": key.Public()})
	create := productCommand(agent, "-create-retained-claim")
	create.Stdin = bytes.NewReader(newPayload)
	created, e := create.CombinedOutput()
	var createdSummary struct {
		State   string `json:"state"`
		Install bool   `json:"can_install"`
		Start   bool   `json:"can_start"`
		Clear   bool   `json:"can_clear_pending"`
	}
	if e != nil || json.Unmarshal(created, &createdSummary) != nil || createdSummary.State != "verified_new_attempt_retained" || createdSummary.Install || createdSummary.Start || createdSummary.Clear {
		t.Fatal("normal product create", e, string(created))
	}
	var active string
	if e = s.st.Pool.QueryRow(t.Context(), `SELECT status FROM gateways WHERE id=$1`, gw3).Scan(&active); e != nil || active != "active" || !claimRedeemed(t, s, code3) {
		t.Fatal("normal product claim not committed", e)
	}
	for _, secret := range []string{code3, "PRIVATE KEY", "ingest_token", "client_cert_pem", "csr_pem"} {
		if bytes.Contains(created, []byte(secret)) {
			t.Fatal("create summary leaked sensitive data")
		}
	}
	if _, e = os.Stat(filepath.Join(newDir, "candidate-response.json")); e != nil {
		t.Fatal("normal create result not retained", e)
	}
	// Prepare only synthetic review evidence, never product-created approval.
	// The review opens no installation targets and cannot permit installation.
	intentBytes, e := os.ReadFile(filepath.Join(newDir, "candidate-intent.json"))
	if e != nil {
		t.Fatal(e)
	}
	var retainedIntent struct{ ID string }
	if json.Unmarshal(intentBytes, &retainedIntent) != nil {
		t.Fatal("intent")
	}
	digest := sha256.Sum256(intentBytes)
	targets := map[string]string{"Binary": filepath.Join(installed, "bin/edge-agent"), "Config": filepath.Join(installed, "etc/edge-agent.yaml"), "State": filepath.Join(installed, "state"), "Unit": filepath.Join(installed, "unit/hexmon-edge.service")}
	plan := map[string]any{"Version": 1, "Attempt": retainedIntent.ID, "IntentSHA256": hex.EncodeToString(digest[:]), "Phase": "prepared", "Targets": targets}
	planPath := filepath.Join(newDir, "candidate-install-plan.json")
	installInput, _ := json.Marshal(map[string]any{"Dir": newDir, "Server": lossServer.URL, "Serial": "normal-product-serial", "DeviceCA": string(ca), "ExpectedTenant": "itest-product-normal-create", "ResponseKey": key.Public(), "Targets": targets})
	reviewInstall := func(want bool) {
		t.Helper()
		child := productCommand(agent, "-review-staged-install")
		child.Stdin = bytes.NewReader(installInput)
		out, e := child.CombinedOutput()
		if !want {
			if e == nil {
				t.Fatal("invalid install binding accepted", string(out))
			}
			return
		}
		var summary struct {
			State   string `json:"state"`
			Install bool   `json:"can_install"`
			Start   bool   `json:"can_start"`
			Clear   bool   `json:"can_clear_pending"`
		}
		if e != nil || json.Unmarshal(out, &summary) != nil || summary.State != "prepared_install_binding_verified" || summary.Install || summary.Start || summary.Clear {
			t.Fatal("install review", e, string(out))
		}
		for _, secret := range []string{code3, "PRIVATE KEY", "ingest_token", "client_cert_pem", "csr_pem", newDir} {
			if bytes.Contains(out, []byte(secret)) {
				t.Fatal("installer review leaked private material")
			}
		}
	}
	reviewInstall(false) // no phase record, never fabricate or repair one
	validPlan, _ := json.Marshal(plan)
	for _, phase := range []string{"copying", "installed", "complete", ""} {
		plan["Phase"] = phase
		raw, _ := json.Marshal(plan)
		if e = os.WriteFile(planPath, raw, 0600); e != nil {
			t.Fatal(e)
		}
		reviewInstall(false)
	}
	for _, field := range []string{"Attempt", "IntentSHA256"} {
		bad := map[string]any{}
		for k, v := range plan {
			bad[k] = v
		}
		bad["Phase"] = "prepared"
		bad[field] = "wrong"
		raw, _ := json.Marshal(bad)
		if e = os.WriteFile(planPath, raw, 0600); e != nil {
			t.Fatal(e)
		}
		reviewInstall(false)
	}
	for _, badRaw := range [][]byte{[]byte(`{"Version":`), bytes.Replace(validPlan, []byte(`"Targets":{`), []byte(`"Targets":{"Binary":"/wrong",`), 1), bytes.Replace(validPlan, []byte(`"Version":1`), []byte(`"Version":1,"Version":1`), 1)} {
		if e = os.WriteFile(planPath, badRaw, 0600); e != nil {
			t.Fatal(e)
		}
		reviewInstall(false)
		unchanged, e := os.ReadFile(planPath)
		if e != nil || !bytes.Equal(unchanged, badRaw) {
			t.Fatal("partial plan changed")
		}
	}
	wrongTargets := map[string]string{}
	for k, v := range targets {
		wrongTargets[k] = v
	}
	wrongTargets["Binary"] = "/unrelated/edge-agent"
	plan["Phase"] = "prepared"
	plan["Targets"] = wrongTargets
	badTargetPlan, _ := json.Marshal(plan)
	if e = os.WriteFile(planPath, badTargetPlan, 0600); e != nil {
		t.Fatal(e)
	}
	reviewInstall(false)
	if e = os.WriteFile(planPath, validPlan, 0600); e != nil {
		t.Fatal(e)
	}
	reviewInstall(false)
	afterPlan, e := os.ReadFile(planPath)
	if e != nil || !bytes.Equal(afterPlan, validPlan) {
		t.Fatal("review changed plan")
	}
	t.Log("PASS actual installer read-only staged install binding: absent/partial/later phases, wrong attempt/digest refuse; unbound injected plan remains refused with no install/start/clear; installed sentinels and host tools unchanged")
	// A fresh bound enrollment must never target an already identified install.
	freshTargetState := filepath.Join(installed, "fresh-unidentified-state")
	if e = os.Mkdir(freshTargetState, 0700); e != nil {
		t.Fatal(e)
	}
	targets["State"] = freshTargetState
	// Use actual product-prepared payload paths, not caller-created file
	// stand-ins, as the baseline for the bound installer operation.
	preparedDir := filepath.Join(parent, "private-install-payload")
	preparedInput, _ := json.Marshal(map[string]any{"Dir": preparedDir})
	if os.Getenv("HEX_NATIVE_COMMISSIONING_MANAGER") == "" {
		preparedChild := productCommand(agent, "-prepare-private-install")
		preparedChild.Stdin = bytes.NewReader(preparedInput)
		preparedOutput, e := preparedChild.CombinedOutput()
		var preparedSummary struct {
			State   string `json:"state"`
			Install bool   `json:"can_install"`
			Start   bool   `json:"can_start"`
			Clear   bool   `json:"can_clear_pending"`
		}
		if e != nil || json.Unmarshal(preparedOutput, &preparedSummary) != nil || preparedSummary.State != "private_payload_prepared" || preparedSummary.Install || preparedSummary.Start || preparedSummary.Clear {
			t.Fatal("actual private installer preparation", e, string(preparedOutput))
		}
	}
	acceptParent := filepath.Join(parent, "private-destination")
	if e = os.Mkdir(acceptParent, 0700); e != nil {
		t.Fatal(e)
	}
	targets = map[string]string{"Binary": filepath.Join(preparedDir, "prepared-agent"), "Config": filepath.Join(preparedDir, "prepared-config.yaml"), "State": filepath.Join(preparedDir, "prepared-state"), "Unit": filepath.Join(preparedDir, "prepared-unit.service"), "Destination": filepath.Join(acceptParent, "accepted")}
	// Final layout selected before creation, never inferred after enrollment.
	finalParent := filepath.Join(parent, "final-deployment")
	if e = os.Mkdir(finalParent, 0700); e != nil {
		t.Fatal(e)
	}
	if os.Getenv("HEX_NATIVE_COMMISSIONING_MANAGER") == "" {
		targets["DeploymentRoot"] = filepath.Join(finalParent, "payload")
		targets["DeploymentState"] = filepath.Join(finalParent, "state")
		targets["DeploymentUnit"] = filepath.Join(finalParent, "hex.service")
		targets["ServiceUID"], targets["ServiceGID"], targets["Activation"] = "10001", "10002", "stopped-only"
	}
	t.Cleanup(func() {
		entries, err := os.ReadDir(finalParent)
		if err != nil || len(entries) != 0 {
			t.Error("binding/assembly touched final deployment destinations", err)
		}
	})
	freshTargetState = targets["State"]
	boundMux := http.NewServeMux()
	boundStop, e := registerCandidateEnrollment(t.Context(), boundMux, cfg)
	if e != nil {
		t.Fatal(e)
	}
	defer boundStop()
	var receiptMux http.Handler
	var receiptToken string
	var nativeInputPath string
	var nativeAnchors string
	managerRun := func(action string, extra string) *exec.Cmd {
		manager := os.Getenv("HEX_NATIVE_COMMISSIONING_MANAGER")
		cmd := exec.Command("unshare", "--user", "--map-user=1001", "--map-group=1001", "bash", manager, "commissioning")
		cmd.Stdin = strings.NewReader(nativeInputPath + "\n" + nativeAnchors + filepath.Join(parent, "normal-bound-attempt") + "\n" + action + "\n" + extra)
		return cmd
	}
	var nativeDropOnce atomic.Bool
	var boundRequests atomic.Int32
	var changeTargetDuringReply atomic.Bool
	boundServer := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		boundRequests.Add(1)
		if r.URL.Path == "/candidate/claim" && nativeDropOnce.Swap(false) {
			capture := httptest.NewRecorder()
			boundMux.ServeHTTP(capture, r)
			conn, _, e := w.(http.Hijacker).Hijack()
			if e != nil {
				t.Error(e)
				return
			}
			conn.Close()
			return
		}
		if changeTargetDuringReply.Swap(false) {
			capture := httptest.NewRecorder()
			boundMux.ServeHTTP(capture, r)
			if e := os.WriteFile(targets["Config"], []byte("changed during verified server response"), 0600); e != nil {
				t.Error(e)
			}
			for k, vs := range capture.Header() {
				for _, v := range vs {
					w.Header().Add(k, v)
				}
			}
			w.WriteHeader(capture.Code)
			_, _ = w.Write(capture.Body.Bytes())
			return
		}
		boundMux.ServeHTTP(w, r)
	}))
	if os.Getenv("HEX_ACCEPTED_RUNTIME_MOSQUITTO") != "" {
		boundServer.Listener.Close()
		l, e := net.Listen("tcp", "10.203.246.1:0")
		if e != nil {
			t.Fatal(e)
		}
		boundServer.Listener = l
		k, e := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		if e != nil {
			t.Fatal(e)
		}
		cert := &x509.Certificate{SerialNumber: big.NewInt(time.Now().UnixNano()), Subject: pkix.Name{CommonName: "synthetic commissioning transport"}, IPAddresses: []net.IP{net.ParseIP("10.203.246.1")}, NotBefore: time.Now().Add(-time.Minute), NotAfter: time.Now().Add(time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
		der, e := x509.CreateCertificate(rand.Reader, cert, cert, &k.PublicKey, k)
		if e != nil {
			t.Fatal(e)
		}
		boundServer.TLS = &tls.Config{Certificates: []tls.Certificate{{Certificate: [][]byte{der}, PrivateKey: k}}}
	}
	boundServer.StartTLS()
	defer boundServer.Close()
	boundTransport := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: boundServer.Certificate().Raw})
	boundCode, _ := seedCandidateFixtureClaim(t, s, "itest-bound-normal", "bound-normal-serial", func(t *testing.T, s *server, tenant, serial string) (string, string) {
		ctx := t.Context()
		for _, q := range []string{`INSERT INTO tenants(id,name)VALUES($1,$1)`, `INSERT INTO sites(id,tenant_id,name)VALUES($1||'-site',$1,'Commissioning')`, `INSERT INTO users(id,tenant_id,email,display_name,role)VALUES($1||'-user',$1,$1||'@example.invalid','Synthetic operator','operator')`} {
			if _, e := s.st.Pool.Exec(ctx, q, tenant); e != nil {
				t.Fatal(e)
			}
		}
		t.Cleanup(func() {
			if _, e := s.st.Pool.Exec(context.Background(), `DELETE FROM audit_log WHERE tenant_id=$1 AND actor=$1||'-user'`, tenant); e != nil {
				t.Error(e)
			}
			if _, e := s.st.Pool.Exec(context.Background(), `DELETE FROM users WHERE id=$1||'-user' AND tenant_id=$1`, tenant); e != nil {
				t.Error(e)
			}
		})
		s.secret = append([]byte(nil), cfg.Issuer.TokenKey...)
		if os.Getenv("HEX_ACCEPTED_RUNTIME_MOSQUITTO") != "" {
			if e := configureCommissioning(s, boundServer.URL, string(boundTransport), cfg); e != nil {
				t.Fatal(e)
			}
		}
		api, mux := http.NewServeMux(), http.NewServeMux()
		registerEnrollmentMint(api, s)
		api.HandleFunc("GET /v1/sites", s.listSites)
		api.HandleFunc("GET /v1/gateways", s.listGateways)
		api.HandleFunc("GET /v1/me", s.me)
		api.HandleFunc("GET /v1/branding", s.getBranding)
		api.HandleFunc("GET /v1/edge-packages", s.edgePackages)
		api.HandleFunc("GET /v1/system/endpoints", s.listEndpoints)
		registerAuthenticatedAPI(mux, api, s)
		receiptMux = mux
		token, e := jwt.NewWithClaims(jwt.SigningMethodHS256, auth.Claims{TenantID: tenant, Role: "operator", RegisteredClaims: jwt.RegisteredClaims{Subject: tenant + "-user", ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour))}}).SignedString(s.secret)
		if e != nil {
			t.Fatal(e)
		}
		receiptToken = token
		req := httptest.NewRequest("POST", "https://example.invalid/v1/enrollment/tokens", strings.NewReader(`{"site_id":"itest-bound-normal-site","serial":"bound-normal-serial","ttl_hours":1,"kind":"edge"}`))
		req.Header.Set("Authorization", "Bearer "+token)
		if s.commissioning != nil {
			req.Header.Set("X-HexThings-Commissioning", "1")
		}
		if s.commissioning != nil {
			if _, e := s.st.Pool.Exec(t.Context(), `ALTER TABLE enrollment_attempt_candidate ALTER COLUMN response DROP NOT NULL`); e != nil {
				t.Fatal(e)
			}
			refusal := httptest.NewRecorder()
			copy := req.Clone(req.Context())
			copy.Body = io.NopCloser(strings.NewReader(`{"site_id":"itest-bound-normal-site","serial":"bound-normal-serial","ttl_hours":1,"kind":"edge"}`))
			mux.ServeHTTP(refusal, copy)
			if refusal.Code != 409 {
				t.Fatal("live readiness drift accepted", refusal.Code)
			}
			var n int
			if e := s.st.Pool.QueryRow(t.Context(), `SELECT count(*)FROM gateways WHERE serial=$1`, serial).Scan(&n); e != nil || n != 0 {
				t.Fatal("readiness refusal wrote gateway", e, n)
			}
			if _, e := s.st.Pool.Exec(t.Context(), `ALTER TABLE enrollment_attempt_candidate ALTER COLUMN response SET NOT NULL`); e != nil {
				t.Fatal(e)
			}
		}

		w := httptest.NewRecorder()
		if controller := os.Getenv("HEX_NATIVE_UI_CONTROLLER"); controller != "" {
			boundMux.Handle("/v1/", mux)
			dist := os.Getenv("HEX_NATIVE_UI_DIST")
			fs := http.FileServer(http.Dir(dist))
			boundMux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/edge-setup" {
					http.ServeFile(w, r, filepath.Join(dist, "index.html"))
					return
				}
				fs.ServeHTTP(w, r)
			})
			nativeInputPath = filepath.Join(parent, "browser-private-input.json")
			sessionPath := filepath.Join(parent, "browser-session.json")
			session, _ := json.Marshal(map[string]string{"origin": boundServer.URL, "token": token, "download": nativeInputPath, "serial": serial})
			if e := os.WriteFile(sessionPath, session, 0600); e != nil {
				t.Fatal(e)
			}
			t.Cleanup(func() {
				cmd := exec.Command("python", controller, "cleanup", sessionPath)
				if out, e := cmd.CombinedOutput(); e != nil {
					t.Error("browser teardown", e, string(out))
				}
			})
			cmd := exec.Command("python", controller, "mint", sessionPath)
			if out, e := cmd.CombinedOutput(); e != nil {
				t.Fatal("actual browser mint/download", e, string(out))
			}
			body, e := os.ReadFile(nativeInputPath)
			if e != nil {
				t.Fatal(e)
			}
			var handoff map[string]any
			if json.Unmarshal(body, &handoff) != nil {
				t.Fatal("browser handoff")
			}
			code, _ := handoff["Code"].(string)
			var gateway string
			if e := s.st.Pool.QueryRow(ctx, `SELECT gateway_id FROM enrollment_tokens WHERE code_hash=$1`, enroll.Hash(code)).Scan(&gateway); e != nil {
				t.Fatal(e)
			}
			nativeAnchors = trustFingerprint(boundTransport) + "\n" + trustFingerprint(ca) + "\n" + trustFingerprint(key.Public().(ed25519.PublicKey)) + "\n"
			t.Log("PASS actual authenticated browser UI mint and downloaded exact private input; independent controller anchor generated before transfer")
			return code, gateway
		}
		mux.ServeHTTP(w, req)
		if w.Code != 201 {
			t.Fatal("real authenticated mint", w.Code, w.Body.String())
		}
		var minted struct {
			Code    string    `json:"claim_code"`
			Gateway string    `json:"gateway_id"`
			Expires time.Time `json:"expires_at"`
		}
		if json.Unmarshal(w.Body.Bytes(), &minted) != nil || minted.Code == "" || time.Until(minted.Expires) > time.Hour {
			t.Fatal("mint contract")
		}
		if s.commissioning != nil {
			var response struct {
				Input struct {
					Server, Serial, Code, TransportCA, DeviceCA, ExpectedTenant string
					ResponseKey                                                 ed25519.PublicKey
				} `json:"retained_input"`
			}
			if json.Unmarshal(w.Body.Bytes(), &response) != nil || response.Input.Server != boundServer.URL || response.Input.Serial != serial || response.Input.Code != minted.Code || response.Input.TransportCA != string(boundTransport) || response.Input.DeviceCA != string(ca) || response.Input.ExpectedTenant != tenant || !bytes.Equal(response.Input.ResponseKey, key.Public().(ed25519.PublicKey)) {
				t.Fatal("actual handoff trust/input mismatch")
			}
			if w.Header().Get("Cache-Control") != "no-store" || bytes.Contains(w.Body.Bytes(), []byte("enroll_string")) {
				t.Fatal("unsafe handoff delivery")
			}
		}
		return minted.Code, minted.Gateway
	})
	boundDir := filepath.Join(parent, "normal-bound-attempt")
	boundPayload, _ := json.Marshal(map[string]any{"Dir": boundDir, "Server": boundServer.URL, "Code": boundCode, "Serial": "bound-normal-serial", "TransportCA": string(boundTransport), "DeviceCA": string(ca), "ExpectedTenant": "itest-bound-normal", "ResponseKey": key.Public(), "Targets": targets})
	boundChild := productCommand(agent, "-create-staged-install")
	boundChild.Stdin = bytes.NewReader(boundPayload)
	if os.Getenv("HEX_NATIVE_COMMISSIONING_MANAGER") != "" {
		// Cancellation after reviewed input does not create the retained destination.
		cancelled := managerRun("CANCEL", "")
		if out, e := cancelled.CombinedOutput(); e == nil || bytes.Contains(out, []byte(boundCode)) {
			t.Fatal("manager cancel/secret refusal")
		}
		if _, e := os.Stat(boundDir); !os.IsNotExist(e) {
			t.Fatal("cancel wrote retained directory")
		}
		root := exec.Command("bash", os.Getenv("HEX_NATIVE_COMMISSIONING_MANAGER"), "commissioning")
		root.Stdin = strings.NewReader("/unread-secret-path\n")
		if out, e := root.CombinedOutput(); e == nil || !bytes.Contains(out, []byte("No input was read")) {
			t.Fatal("root manager refusal", e, string(out))
		}
		nativeDropOnce.Store(true)
		boundChild = managerRun("CREATE BOUND", preparedDir+"\n"+acceptParent+"\naccepted\nPREPARE NEW PRIVATE PAYLOAD\nCREATE BOUND ATTEMPT\n")
	}
	if nativeManagerRun := os.Getenv("HEX_NATIVE_COMMISSIONING_MANAGER") != ""; nativeManagerRun {
		// The fixed manager command line carries neither claim nor trust payload.
		for _, arg := range boundChild.Args {
			if strings.Contains(arg, boundCode) {
				t.Fatal("manager secret argv")
			}
		}
	}
	boundOut, e := boundChild.CombinedOutput()
	if os.Getenv("HEX_NATIVE_COMMISSIONING_MANAGER") != "" {
		if e == nil {
			t.Fatal("lost native reply reported success")
		}
		var keyBefore []byte
		keyBefore, e = os.ReadFile(filepath.Join(boundDir, "identity.key"))
		if e != nil {
			t.Fatal(e)
		}
		if !claimRedeemed(t, s, boundCode) {
			t.Fatal("lost reply did not commit")
		}
		if d := os.Getenv("HEX_NATIVE_EVIDENCE_DIR"); d != "" {
			os.WriteFile(filepath.Join(d, "manager-lost-reply-terminal.txt"), boundOut, 0600)
		}
		child := managerRun("RECOVER SAME", "")
		recovered, recoverErr := child.CombinedOutput()
		if recoverErr != nil {
			t.Fatal("actual manager explicit lost reply recovery", recoverErr, string(recovered))
		}
		keyAfter, e := os.ReadFile(filepath.Join(boundDir, "identity.key"))
		if e != nil || !bytes.Equal(keyBefore, keyAfter) {
			t.Fatal("manager recovery key changed")
		}
		boundOut = append(boundOut, recovered...)
		e = nil
		t.Log("PASS actual manager lost reply after server commit refuses success; separate explicit same-attempt recovery retains exact key, no create replay")
	}

	var boundSummary struct {
		State   string `json:"state"`
		Install bool   `json:"can_install"`
		Start   bool   `json:"can_start"`
		Clear   bool   `json:"can_clear_pending"`
	}
	nativeManager := os.Getenv("HEX_NATIVE_COMMISSIONING_MANAGER") != ""
	boundValid := json.Unmarshal(boundOut, &boundSummary) == nil && boundSummary.State == "verified_install_attempt_retained" && !boundSummary.Install && !boundSummary.Start && !boundSummary.Clear
	if nativeManager {
		boundValid = bytes.Contains(boundOut, []byte("verified_recovery_retained"))
	}
	if nativeManager {
		if dir := os.Getenv("HEX_NATIVE_EVIDENCE_DIR"); dir != "" {
			if e := os.WriteFile(filepath.Join(dir, "manager-create-terminal.txt"), boundOut, 0600); e != nil {
				t.Fatal(e)
			}
		}
		t.Log("PASS actual non-root existing manager prompt process, fixed preparation review/create-bound child, secret input only via stdin, exact local selected targets")
	}
	if e != nil || !boundValid || !claimRedeemed(t, s, boundCode) {
		t.Fatal("normal bound staging", e, string(boundOut))
	}
	// A failed postcommit broker file write cannot undo/redeliver credentials.
	oldACL, hadACL := os.LookupEnv("BROKER_ACL_FILE")
	if e := os.Setenv("BROKER_ACL_FILE", filepath.Join(parent, "absent-acl-parent", "acl")); e != nil {
		t.Fatal(e)
	}
	aclFailure := s.regenerateBrokerACL(t.Context())
	if hadACL {
		os.Setenv("BROKER_ACL_FILE", oldACL)
	} else {
		os.Unsetenv("BROKER_ACL_FILE")
	}
	if aclFailure == nil || !claimRedeemed(t, s, boundCode) {
		t.Fatal("postcommit ACL failure must preserve redemption")
	}
	var committedCount int
	if e := s.st.Pool.QueryRow(t.Context(), `SELECT count(*)FROM enrollment_attempt_candidate WHERE gateway_id=(SELECT gateway_id FROM enrollment_tokens WHERE code_hash=$1)`, enroll.Hash(boundCode)).Scan(&committedCount); e != nil || committedCount != 1 {
		t.Fatal("postcommit failure changed exact attempt", e, committedCount)
	}
	t.Log("PASS ACL file failure after committed issue preserves one consumed code/exact retained attempt; later existing explicit recovery/runtime assembly reconciles same key without issuer replay")
	var storedDeadline time.Time
	var storedResponse []byte
	if e := s.st.Pool.QueryRow(t.Context(), `SELECT expires_at,response FROM enrollment_attempt_candidate WHERE gateway_id=(SELECT gateway_id FROM enrollment_tokens WHERE code_hash=$1)`, enroll.Hash(boundCode)).Scan(&storedDeadline, &storedResponse); e != nil {
		t.Fatal(e)
	}
	var outer struct {
		Manifest []byte `json:"manifest"`
	}
	var signed struct {
		Expires int64 `json:"expires"`
	}
	if json.Unmarshal(storedResponse, &outer) != nil || json.Unmarshal(outer.Manifest, &signed) != nil || storedDeadline.Unix() != signed.Expires || time.Until(storedDeadline) > 45*time.Minute {
		t.Fatal("effective recovery deadline mismatch")
	}
	if _, e := s.st.Pool.Exec(t.Context(), `UPDATE enrollment_attempt_candidate SET expires_at=now()-interval '1 second' WHERE gateway_id=(SELECT gateway_id FROM enrollment_tokens WHERE code_hash=$1)`, enroll.Hash(boundCode)); e != nil {
		t.Fatal(e)
	}
	expired := productCommand(agent, "-recover-retained-claim")
	expired.Stdin = bytes.NewReader(boundPayload)
	if e := expired.Run(); e == nil {
		t.Fatal("expired stored attempt recovered")
	}
	if _, e := s.st.Pool.Exec(t.Context(), `UPDATE enrollment_attempt_candidate SET expires_at=$2 WHERE gateway_id=(SELECT gateway_id FROM enrollment_tokens WHERE code_hash=$1)`, enroll.Hash(boundCode), storedDeadline); e != nil {
		t.Fatal(e)
	}
	for _, secret := range []string{boundCode, "PRIVATE KEY", "ingest_token", "client_cert_pem", "csr_pem", boundDir} {
		if secret != boundDir && bytes.Contains(boundOut, []byte(secret)) {
			t.Fatal("bound create leaked private material")
		}
	}
	boundChild = productCommand(agent, "-create-staged-install")
	boundChild.Stdin = bytes.NewReader(boundPayload)
	if e = boundChild.Run(); e == nil {
		t.Fatal("bound staging replay accepted")
	}
	// A changed installed input refuses explicit recovery before HTTP, and a
	// later restore still needs a fresh explicit check rather than cached trust.
	boundRecoverPayload, _ := json.Marshal(map[string]any{"Dir": boundDir, "Server": boundServer.URL, "Code": boundCode, "Serial": "bound-normal-serial", "TransportCA": string(boundTransport), "DeviceCA": string(ca), "ExpectedTenant": "itest-bound-normal", "ResponseKey": key.Public()})
	configPath := targets["Config"]
	configBefore, e := os.ReadFile(configPath)
	if e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(configPath, []byte("changed installed config"), 0600); e != nil {
		t.Fatal(e)
	}
	// Existing/partial identity appearing after preparation also blocks recovery.
	appearedIdentity := filepath.Join(freshTargetState, "identity.key")
	if e = os.WriteFile(appearedIdentity, []byte("partial existing identity"), 0600); e != nil {
		t.Fatal(e)
	}
	identityRequests := boundRequests.Load()
	boundChild = productCommand(agent, "-recover-retained-claim")
	boundChild.Stdin = bytes.NewReader(boundRecoverPayload)
	if e = boundChild.Run(); e == nil || boundRequests.Load() != identityRequests {
		t.Fatal("new target identity allowed request")
	}
	if e = os.Remove(appearedIdentity); e != nil {
		t.Fatal(e)
	}
	beforeChangedRequests := boundRequests.Load()
	boundChild = productCommand(agent, "-recover-retained-claim")
	boundChild.Stdin = bytes.NewReader(boundRecoverPayload)
	if e = boundChild.Run(); e == nil || boundRequests.Load() != beforeChangedRequests {
		t.Fatal("target drift reached network")
	}
	if e = os.WriteFile(configPath, configBefore, 0600); e != nil {
		t.Fatal(e)
	}
	boundChild = productCommand(agent, "-recover-retained-claim")
	boundChild.Stdin = bytes.NewReader(boundRecoverPayload)
	if nativeManager {
		boundChild = managerRun("RECOVER SAME", "")
	}
	if e = boundChild.Run(); e != nil {
		t.Fatal("restored unchanged target bytes recovery refused", e)
	}
	manifestPath := filepath.Join(preparedDir, "prepared-manifest.json")
	manifestBefore, e := os.ReadFile(manifestPath)
	if e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(manifestPath, []byte(`{"Version":1`), 0600); e != nil {
		t.Fatal(e)
	}
	manifestRequests := boundRequests.Load()
	boundChild = productCommand(agent, "-recover-retained-claim")
	boundChild.Stdin = bytes.NewReader(boundRecoverPayload)
	if e = boundChild.Run(); e == nil || boundRequests.Load() != manifestRequests {
		t.Fatal("partial preparation manifest reached network")
	}
	partialManifest, e := os.ReadFile(manifestPath)
	if e != nil || string(partialManifest) != `{"Version":1` {
		t.Fatal("partial preparation manifest repaired")
	}
	if e = os.WriteFile(manifestPath, manifestBefore, 0600); e != nil {
		t.Fatal(e)
	}
	changeTargetDuringReply.Store(true)
	beforeChangedRequests = boundRequests.Load()
	boundChild = productCommand(agent, "-recover-retained-claim")
	boundChild.Stdin = bytes.NewReader(boundRecoverPayload)
	if e = boundChild.Run(); e == nil || boundRequests.Load() != beforeChangedRequests+1 {
		t.Fatal("in-flight target replacement accepted")
	}
	if e = os.WriteFile(configPath, configBefore, 0600); e != nil {
		t.Fatal(e)
	}
	boundChild = productCommand(agent, "-recover-retained-claim")
	boundChild.Stdin = bytes.NewReader(boundRecoverPayload)
	if e = boundChild.Run(); e != nil {
		t.Fatal("explicit read-only recovery after target restore refused", e)
	}
	acceptPayload, _ := json.Marshal(map[string]any{"Dir": boundDir, "Server": boundServer.URL, "Code": boundCode, "Serial": "bound-normal-serial", "TransportCA": string(boundTransport), "DeviceCA": string(ca), "ExpectedTenant": "itest-bound-normal", "ResponseKey": key.Public(), "DestinationParent": acceptParent, "DestinationName": "accepted"})
	wrongAcceptPayload := bytes.Replace(acceptPayload, []byte(`"DestinationName":"accepted"`), []byte(`"DestinationName":"other"`), 1)
	wrongAccept := productCommand(agent, "-accept-private-identity")
	wrongAccept.Stdin = bytes.NewReader(wrongAcceptPayload)
	beforeWrongDestination := boundRequests.Load()
	if e = wrongAccept.Run(); e == nil || boundRequests.Load() != beforeWrongDestination {
		t.Fatal("unbound destination contacted server")
	}
	acceptChild := productCommand(agent, "-accept-private-identity")
	acceptChild.Stdin = bytes.NewReader(acceptPayload)
	if nativeManager {
		time.Sleep(13 * time.Second) // real shared recovery limiter, never bypass
		acceptChild = managerRun("ACCEPT PRIVATE", acceptParent+"\naccepted\nACCEPT PREBOUND PRIVATE IDENTITY\n")
	}
	acceptOut, e := acceptChild.CombinedOutput()
	var acceptSummary struct {
		State   string `json:"state"`
		Install bool   `json:"can_install"`
		Start   bool   `json:"can_start"`
		Clear   bool   `json:"can_clear_pending"`
	}
	acceptValid := json.Unmarshal(acceptOut, &acceptSummary) == nil && acceptSummary.State == "private_identity_accepted_service_blocked" && !acceptSummary.Install && !acceptSummary.Start && !acceptSummary.Clear
	if nativeManager {
		acceptValid = bytes.Contains(acceptOut, []byte("private_identity_accepted_service_blocked"))
	}
	if nativeManager {
		if dir := os.Getenv("HEX_NATIVE_EVIDENCE_DIR"); dir != "" {
			if e := os.WriteFile(filepath.Join(dir, "manager-accept-terminal.txt"), acceptOut, 0600); e != nil {
				t.Fatal(e)
			}
		}
	}
	if e != nil || !acceptValid {
		t.Fatal("actual private identity acceptance", e, string(acceptOut))
	}
	originalKey, e := os.ReadFile(filepath.Join(boundDir, "identity.key"))
	if e != nil {
		t.Fatal(e)
	}
	acceptedKey, e := os.ReadFile(filepath.Join(acceptParent, "accepted/identity.key"))
	if e != nil || !bytes.Equal(originalKey, acceptedKey) {
		t.Fatal("acceptance changed private key", e)
	}
	if _, e = os.Stat(filepath.Join(acceptParent, "accepted.complete.json")); e != nil {
		t.Fatal("missing acceptance receipt", e)
	}
	for _, secret := range []string{boundCode, "PRIVATE KEY", "ingest_token", "client_cert_pem", acceptParent} {
		if secret != acceptParent && bytes.Contains(acceptOut, []byte(secret)) {
			t.Fatal("acceptance output leaked material")
		}
	}
	acceptedJSON, e := os.ReadFile(filepath.Join(acceptParent, "accepted/identity.json"))
	if e != nil {
		t.Fatal(e)
	}
	var mappedIdentity struct {
		Gateway string `json:"gateway_id"`
	}
	if json.Unmarshal(acceptedJSON, &mappedIdentity) != nil {
		t.Fatal("accepted identity")
	}
	mappingInputDir := filepath.Join(parent, "mapping-inputs")
	if e = os.Mkdir(mappingInputDir, 0700); e != nil {
		t.Fatal(e)
	}
	serverConfig := fmt.Sprintf("gateway_id: %s\ntenant_id: itest-bound-normal\nserial: bound-normal-serial\nmqtt: {host: broker.example.invalid, port: 8883, tls: true}\nautodetect: {enabled: false}\ndevices: []\n", mappedIdentity.Gateway)
	if e = os.WriteFile(filepath.Join(mappingInputDir, "server-config.yaml"), []byte(serverConfig), 0600); e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(filepath.Join(mappingInputDir, "broker-ca.pem"), ca, 0600); e != nil {
		t.Fatal(e)
	}
	mappingInput, _ := json.Marshal(map[string]any{"IdentityDir": filepath.Join(acceptParent, "accepted"), "InputDir": mappingInputDir, "Serial": "bound-normal-serial", "Tenant": "itest-bound-normal", "Binary": "/opt/hexthings/edge-agent", "ConfigPath": "/etc/hexthings/runtime.yaml", "StateDir": "/var/lib/hexthings-runtime", "User": "hexmon"})
	mappingChild := productCommand(agent, "-review-runtime-mapping")
	mappingChild.Stdin = bytes.NewReader(mappingInput)
	mappingOut, e := mappingChild.CombinedOutput()
	var mapping struct {
		ConfigYAML, Unit string
		CanStart         bool
	}
	if e != nil || json.Unmarshal(mappingOut, &mapping) != nil || mapping.CanStart || !strings.Contains(mapping.Unit, "-identity-dir "+filepath.Join(acceptParent, "accepted")) || strings.Contains(mapping.Unit, "[Install]") || !strings.Contains(mapping.ConfigYAML, "identity.key") {
		t.Fatal("actual accepted-identity mapping", e, string(mappingOut))
	}
	for _, secret := range []string{boundCode, "PRIVATE KEY", "ingest_token"} {
		if bytes.Contains(mappingOut, []byte(secret)) {
			t.Fatal("mapping leaked secret")
		}
	}
	if !nativeManager {
		assemblyInput, _ := json.Marshal(map[string]any{"Retained": json.RawMessage(boundRecoverPayload), "InputDir": mappingInputDir, "Serial": "bound-normal-serial", "Tenant": "itest-bound-normal", "User": "hexreview"})
		assemblyRun := func() ([]byte, error) {
			time.Sleep(13 * time.Second) // respect the real shared recovery limiter, no test bypass
			child := productCommand(agent, "-assemble-private-runtime")
			child.Stdin = bytes.NewReader(assemblyInput)
			return child.CombinedOutput()
		}
		for repeat := 0; repeat < 2; repeat++ {
			out, e := assemblyRun()
			var summary struct {
				State                                 string
				CanInstall, CanStart, CanClearPending bool
			}
			if e != nil || json.Unmarshal(out, &summary) != nil || summary.State != "private_runtime_payload_committed" || summary.CanInstall || summary.CanStart || summary.CanClearPending {
				t.Fatal("actual private runtime assembly", e, string(out))
			}
		}
		for _, n := range []string{"agent", "config.yaml", "unit.service"} {
			st, e := os.Stat(filepath.Join(preparedDir, "prepared-state/runtime", n))
			if e != nil || st.Mode().Perm() != 0600 {
				t.Fatal("private nonexecuting runtime payload", n, e)
			}
		}
		handoffRoot := filepath.Join(preparedDir, "prepared-state/handoff")
		for _, n := range []string{"agent", "config.yaml", "unit.service", "broker-ca.pem", "device-ca.pem", "identity.json", "identity.crt", "identity.key"} {
			st, err := os.Stat(filepath.Join(handoffRoot, n))
			if err != nil || st.Mode().Perm() != 0600 {
				t.Fatal("complete private nonexecuting frozen handoff", n, err)
			}
		}
		frozenConfig, err := os.ReadFile(filepath.Join(handoffRoot, "config.yaml"))
		if err != nil || !bytes.Contains(frozenConfig, []byte(targets["DeploymentRoot"]+"/identity/identity.key")) || bytes.Contains(frozenConfig, []byte(acceptParent)) {
			t.Fatal("frozen final config has external selected identity paths", err)
		}
		frozenUnit, err := os.ReadFile(filepath.Join(handoffRoot, "unit.service"))
		if err != nil || !bytes.Contains(frozenUnit, []byte("User=10001\nGroup=10002")) || bytes.Contains(frozenUnit, []byte("[Install]")) {
			t.Fatal("frozen final unit differs", err)
		}
		frozenKey, err := os.ReadFile(filepath.Join(handoffRoot, "identity.key"))
		if err != nil || !bytes.Equal(frozenKey, func() []byte { b, _ := os.ReadFile(filepath.Join(acceptParent, "accepted/identity.key")); return b }()) {
			t.Fatal("frozen identity key differs", err)
		}
		savedRuntimeConfig, e := os.ReadFile(filepath.Join(preparedDir, "prepared-state/runtime/config.yaml"))
		if e != nil {
			t.Fatal(e)
		}
		if e = os.WriteFile(filepath.Join(mappingInputDir, "server-config.yaml"), []byte(strings.Replace(serverConfig, "port: 8883", "port: 8884", 1)), 0600); e != nil {
			t.Fatal(e)
		}
		if out, e := assemblyRun(); e == nil {
			t.Fatal("changed runtime payload replay accepted", string(out))
		}
		afterRuntime, e := os.ReadFile(filepath.Join(preparedDir, "prepared-state/runtime/config.yaml"))
		if e != nil || !bytes.Equal(afterRuntime, savedRuntimeConfig) {
			t.Fatal("runtime refusal changed committed data", e)
		}
		if e = os.WriteFile(filepath.Join(mappingInputDir, "server-config.yaml"), []byte(serverConfig), 0600); e != nil {
			t.Fatal(e)
		}
		assemblyRequests := boundRequests.Load()
		savedAcceptedKey, e := os.ReadFile(filepath.Join(acceptParent, "accepted/identity.key"))
		if e != nil {
			t.Fatal(e)
		}
		if e = os.WriteFile(filepath.Join(acceptParent, "accepted/identity.key"), []byte("partial replaced key"), 0600); e != nil {
			t.Fatal(e)
		}
		if out, e := assemblyRun(); e == nil {
			t.Fatal("changed accepted identity assembled", string(out))
		}
		if e = os.WriteFile(filepath.Join(acceptParent, "accepted/identity.key"), savedAcceptedKey, 0600); e != nil {
			t.Fatal(e)
		}
		var assemblyAttempt string
		var intentDoc struct{ ID string }
		intentBytes, e = os.ReadFile(filepath.Join(boundDir, "candidate-intent.json"))
		if e != nil || json.Unmarshal(intentBytes, &intentDoc) != nil {
			t.Fatal("assembly attempt", e)
		}
		assemblyAttempt = intentDoc.ID
		if _, e = s.st.Pool.Exec(t.Context(), `UPDATE enrollment_attempt_candidate SET revoked=true WHERE attempt_id=$1`, assemblyAttempt); e != nil {
			t.Fatal(e)
		}
		if out, e := assemblyRun(); e == nil {
			t.Fatal("revoked retained runtime accepted", string(out))
		}
		if _, e = s.st.Pool.Exec(t.Context(), `UPDATE enrollment_attempt_candidate SET revoked=false WHERE attempt_id=$1`, assemblyAttempt); e != nil {
			t.Fatal(e)
		}
		if boundRequests.Load() != assemblyRequests+2 {
			t.Fatal("fresh assembly checks bypassed", boundRequests.Load(), assemblyRequests)
		}
		afterRuntime, e = os.ReadFile(filepath.Join(preparedDir, "prepared-state/runtime/config.yaml"))
		if e != nil || !bytes.Equal(afterRuntime, savedRuntimeConfig) {
			t.Fatal("identity/revocation refusal changed runtime", e)
		}
		t.Log("PASS actual .run fresh retained recovery -> creation-bound prepared binary/identity -> atomic private nonexec runtime payload and exact-repeat verification; changed config refuses without overwrite, install/start/clear blocked")
	}
	acceptedRuntimeBrokerSourceProof(t, s, agent, filepath.Join(acceptParent, "accepted"), mappedIdentity.Gateway, "itest-bound-normal", "bound-normal-serial", ca, cak, func(raw []byte) ([]byte, error) {
		child := productCommand(agent, "-review-runtime-mapping")
		child.Stdin = bytes.NewReader(raw)
		return child.CombinedOutput()
	}, func(gateway, event string) {

		if controller := os.Getenv("HEX_NATIVE_UI_CONTROLLER"); controller != "" {
			sessionPath := filepath.Join(parent, "browser-receipt.json")
			b, _ := json.Marshal(map[string]string{"origin": boundServer.URL, "token": receiptToken, "gateway": gateway, "event": event})
			os.WriteFile(sessionPath, b, 0600)
			cmd := exec.Command("python", controller, "receipt", sessionPath)
			if out, e := cmd.CombinedOutput(); e != nil {
				t.Fatal("actual browser exact receipt", e, string(out))
			}
		}
		for _, v := range []struct {
			gateway, event string
			code           int
		}{{gateway, event, 200}, {gateway, event + "-wrong", 404}, {gateway + "-wrong", event, 404}} {
			req := httptest.NewRequest("GET", "https://example.invalid/v1/enrollment/commissioning/receipt?gateway_id="+v.gateway+"&event_id="+v.event, nil)
			req.Header.Set("Authorization", "Bearer "+receiptToken)
			w := httptest.NewRecorder()
			receiptMux.ServeHTTP(w, req)
			if w.Code != v.code {
				t.Fatal("exact authenticated receipt", w.Code, w.Body.String())
			}
			if w.Code == 200 && !bytes.Contains(w.Body.Bytes(), []byte(event)) {
				t.Fatal("wrong exact receipt")
			}
		}
		// Shared middleware and explicit feature state stay default-deny.
		doReceipt := func(token string) int {
			r := httptest.NewRequest("GET", "https://example.invalid/v1/enrollment/commissioning/receipt?gateway_id="+gateway+"&event_id="+event, nil)
			if token != "" {
				r.Header.Set("Authorization", "Bearer "+token)
			}
			w := httptest.NewRecorder()
			receiptMux.ServeHTTP(w, r)
			return w.Code
		}
		wrongTenantToken, e := jwt.NewWithClaims(jwt.SigningMethodHS256, auth.Claims{TenantID: "foreign-tenant", Role: "operator", RegisteredClaims: jwt.RegisteredClaims{Subject: "itest-bound-normal-user", ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour))}}).SignedString(s.secret)
		if e != nil {
			t.Fatal(e)
		}
		if doReceipt(wrongTenantToken) != 403 {
			t.Fatal("cross-tenant session receipt")
		}
		if doReceipt("") != 401 {
			t.Fatal("anonymous receipt")
		}
		if _, e := s.st.Pool.Exec(t.Context(), `INSERT INTO customers(id,tenant_id,name)VALUES('itest-receipt-customer','itest-bound-normal','Receipt fixture')`); e != nil {
			t.Fatal(e)
		}
		t.Cleanup(func() {
			s.st.Pool.Exec(context.Background(), `DELETE FROM user_customer_scope WHERE tenant_id='itest-bound-normal' AND user_id='itest-bound-normal-user'`)
			s.st.Pool.Exec(context.Background(), `DELETE FROM customers WHERE id='itest-receipt-customer' AND tenant_id='itest-bound-normal'`)
		})
		if _, e := s.st.Pool.Exec(t.Context(), `INSERT INTO user_customer_scope(tenant_id,user_id,customer_id)VALUES('itest-bound-normal','itest-bound-normal-user','itest-receipt-customer')`); e != nil {
			t.Fatal(e)
		}
		if doReceipt(receiptToken) != 403 {
			t.Fatal("customer-scoped receipt")
		}
		if _, e := s.st.Pool.Exec(t.Context(), `DELETE FROM user_customer_scope WHERE tenant_id='itest-bound-normal' AND user_id='itest-bound-normal-user'`); e != nil {
			t.Fatal(e)
		}
		saved := s.commissioning
		s.commissioning = nil
		if doReceipt(receiptToken) != 409 {
			t.Fatal("default-off receipt")
		}
		s.commissioning = saved
		if _, e := s.st.Pool.Exec(t.Context(), `UPDATE users SET role='viewer' WHERE id='itest-bound-normal-user'`); e != nil {
			t.Fatal(e)
		}
		if doReceipt(receiptToken) != 403 {
			t.Fatal("viewer receipt")
		}
		if _, e := s.st.Pool.Exec(t.Context(), `UPDATE users SET role='operator',disabled_at=now() WHERE id='itest-bound-normal-user'`); e != nil {
			t.Fatal(e)
		}
		if doReceipt(receiptToken) != 401 {
			t.Fatal("disabled receipt")
		}
		if _, e := s.st.Pool.Exec(t.Context(), `UPDATE users SET disabled_at=NULL WHERE id='itest-bound-normal-user'`); e != nil {
			t.Fatal(e)
		}
		t.Log("PASS authenticated active interactive exact tenant/gateway/event read receipt, mismatch/foreign gateway refused; controller-observed event only")
	})
	// Exercise source refusal through the same actual packaged process and
	// accepted private identity, not only the in-process mapping builder.
	for _, unsafeSource := range []string{
		"devices: [{id: source, client_key: /etc/foreign/identity.key}]",
		"devices: [{id: source, password_env: FOREIGN_DEVICE_PASSWORD}]",
		"devices: [{id: repeated}, {id: repeated}]",
		"devices: [{id: source, points: [{id: repeated}, {id: repeated}]}]",
	} {
		bad := strings.Replace(serverConfig, "devices: []", unsafeSource, 1)
		if e = os.WriteFile(filepath.Join(mappingInputDir, "server-config.yaml"), []byte(bad), 0600); e != nil {
			t.Fatal(e)
		}
		child := productCommand(agent, "-review-runtime-mapping")
		child.Stdin = bytes.NewReader(mappingInput)
		var stdout, stderr bytes.Buffer
		child.Stdout = &stdout
		child.Stderr = &stderr
		if e = child.Run(); e == nil || stdout.Len() != 0 {
			t.Fatal("actual packaged source refusal", e, stdout.String())
		}
		if bytes.Contains(stderr.Bytes(), []byte("FOREIGN_DEVICE_PASSWORD")) || bytes.Contains(stderr.Bytes(), []byte("/etc/foreign/identity.key")) {
			t.Fatal("source error disclosed selector")
		}
	}
	if e = os.WriteFile(filepath.Join(mappingInputDir, "server-config.yaml"), []byte(serverConfig), 0600); e != nil {
		t.Fatal(e)
	}
	if after, e := os.ReadFile(filepath.Join(acceptParent, "accepted/identity.key")); e != nil || !bytes.Equal(after, originalKey) {
		t.Fatal("source refusal changed accepted key", e)
	}
	t.Log("PASS actual packaged mapping refuses foreign credential files/environment selectors and duplicate device/point IDs without config output or accepted-key change")
	t.Log("PASS actual complete installer-bound staging commits once, retains prepared binding and emits no install/start/clear permission; repeat staging refuses")
	faultBin := os.Getenv("HEX_CLAIM_FAULT_AGENT_BIN")
	if faultBin != "" {
		for index, stage := range []string{"after_directory", "after_key", "after_intent", "after_install_plan", "partial_install_plan_write", "after_started", "before_request", "after_verified_response", "after_saved_response", "partial_response_write", "destination_after_promotion", "destination_after_receipt", "destination_after_intent", "destination_after_directory", "destination_after_identity.json", "destination_after_identity.key"} {
			t.Run("process-kill-"+stage, func(t *testing.T) {
				tenant := fmt.Sprintf("itest-process-cut-%d", index)
				serial := fmt.Sprintf("cut-serial-%d", index)
				code, _ := seedCandidate(t, s, tenant, serial)
				cutMux := http.NewServeMux()
				stop, e := registerCandidateEnrollment(t.Context(), cutMux, cfg)
				if e != nil {
					t.Fatal(e)
				}
				defer stop()
				var requests atomic.Int32
				cutServer := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { requests.Add(1); cutMux.ServeHTTP(w, r) }))
				defer cutServer.Close()
				transport := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: cutServer.Certificate().Raw})
				parent := t.TempDir()
				if e = os.Chmod(parent, 0700); e != nil {
					t.Fatal(e)
				}
				dir := filepath.Join(parent, "attempt")
				cutTargets := map[string]string{}
				for k, v := range targets {
					cutTargets[k] = v
				}
				destParent := filepath.Join(parent, "acceptance")
				if e = os.Mkdir(destParent, 0700); e != nil {
					t.Fatal(e)
				}
				cutTargets["Destination"] = filepath.Join(destParent, "accepted")

				payload, _ := json.Marshal(map[string]any{"Dir": dir, "Server": cutServer.URL, "Code": code, "Serial": serial, "TransportCA": string(transport), "DeviceCA": string(ca), "ExpectedTenant": tenant, "ResponseKey": key.Public(), "Targets": cutTargets})
				child := productCommand(faultBin, "-create-staged-install")
				crashStage := stage
				destinationCut := strings.HasPrefix(stage, "destination_")
				if destinationCut {
					crashStage = "after_saved_response"
				}
				child.Env = append(child.Environ(), "HEX_CANDIDATE_TEST_CRASH="+crashStage)
				child.Stdin = bytes.NewReader(payload)
				if e = child.Run(); e == nil {
					t.Fatal("fault child did not stop")
				} else if exit, ok := e.(*exec.ExitError); !ok || exit.ExitCode() != 86 {
					t.Fatal("not intentional process termination", e)
				}
				payload, _ = json.Marshal(map[string]any{"Dir": dir, "Server": cutServer.URL, "Code": code, "Serial": serial, "TransportCA": string(transport), "DeviceCA": string(ca), "ExpectedTenant": tenant, "ResponseKey": key.Public()})
				committed := destinationCut || stage == "after_verified_response" || stage == "after_saved_response" || stage == "partial_response_write"
				if claimRedeemed(t, s, code) != committed {
					t.Fatal("unexpected token state", stage)
				}
				if committed && requests.Load() != 1 || !committed && requests.Load() != 0 {
					t.Fatal("unexpected request count", stage, requests.Load())
				}
				files := map[string][]byte{}
				names, e := os.ReadDir(dir)
				if e != nil {
					t.Fatal(e)
				}
				for _, name := range names {
					files[name.Name()], e = os.ReadFile(filepath.Join(dir, name.Name()))
					if e != nil {
						t.Fatal(e)
					}
				}
				// Production binary ignores the fault environment and rejects a new
				// create before HTTP for every partial/complete retained state.
				child = productCommand(agent, "-create-retained-claim")
				child.Env = append(child.Environ(), "HEX_CANDIDATE_TEST_CRASH="+stage)
				child.Stdin = bytes.NewReader(payload)
				if e = child.Run(); e == nil {
					t.Fatal("retained attempt replay allowed")
				} else if exit, ok := e.(*exec.ExitError); ok && exit.ExitCode() == 86 {
					t.Fatal("fault environment active in ordinary product")
				}
				beforeRequests := requests.Load()
				child = productCommand(agent, "-recover-retained-claim")
				child.Stdin = bytes.NewReader(payload)
				e = child.Run()
				if stage == "partial_response_write" {
					if e == nil || requests.Load() != beforeRequests {
						t.Fatal("partial response overwrite/request allowed", e)
					}
				} else if committed {
					if e != nil {
						t.Fatal("post-commit process cut unrecoverable", e)
					}
					if requests.Load() != beforeRequests+1 {
						t.Fatal("missing explicit recover")
					}
				} else {
					if e == nil {
						t.Fatal("uncommitted cut reported recovered")
					}
					complete := stage == "after_started" || stage == "before_request"
					want := beforeRequests
					if complete {
						want++
					}
					if requests.Load() != want {
						t.Fatal("partial cut contacted server or complete cut skipped recover", stage, requests.Load(), want)
					}
				}
				if stage == "after_saved_response" {
					planBefore := files["candidate-install-plan.json"]
					if e = os.Remove(filepath.Join(dir, "candidate-install-plan.json")); e != nil {
						t.Fatal(e)
					}
					count := requests.Load()
					child = productCommand(agent, "-recover-retained-claim")
					child.Stdin = bytes.NewReader(payload)
					if e = child.Run(); e == nil || requests.Load() != count {
						t.Fatal("deleted required plan allowed request")
					}
					if e = os.WriteFile(filepath.Join(dir, "candidate-install-plan.json"), planBefore, 0600); e != nil {
						t.Fatal(e)
					}
				}
				if committed && stage != "partial_response_write" {
					installPayload, _ := json.Marshal(map[string]any{"Dir": dir, "Server": cutServer.URL, "Serial": serial, "DeviceCA": string(ca), "ExpectedTenant": tenant, "ResponseKey": key.Public(), "Targets": cutTargets})
					count := requests.Load()
					child = productCommand(agent, "-review-staged-install")
					child.Stdin = bytes.NewReader(installPayload)
					out, e := child.CombinedOutput()
					var summary struct {
						State   string `json:"state"`
						Install bool   `json:"can_install"`
						Start   bool   `json:"can_start"`
						Clear   bool   `json:"can_clear_pending"`
					}
					if e != nil || json.Unmarshal(out, &summary) != nil || summary.State != "prepared_install_binding_verified" || summary.Install || summary.Start || summary.Clear || requests.Load() != count {
						t.Fatal("bound recovered installer review", e, string(out))
					}
				}
				if destinationCut {
					acceptPayload, _ := json.Marshal(map[string]any{"Dir": dir, "Server": cutServer.URL, "Code": code, "Serial": serial, "TransportCA": string(transport), "DeviceCA": string(ca), "ExpectedTenant": tenant, "ResponseKey": key.Public(), "DestinationParent": destParent, "DestinationName": "accepted"})
					child = productCommand(faultBin, "-accept-private-identity")
					child.Env = append(child.Environ(), "HEX_CANDIDATE_TEST_CRASH="+stage)
					child.Stdin = bytes.NewReader(acceptPayload)
					if e = child.Run(); e == nil {
						t.Fatal("destination acceptance did not stop")
					} else if exit, ok := e.(*exec.ExitError); !ok || exit.ExitCode() != 86 {
						t.Fatal("acceptance fault exit", e)
					}
					child = productCommand(agent, "-accept-private-identity")
					child.Stdin = bytes.NewReader(acceptPayload)
					if e = child.Run(); e != nil {
						t.Fatal("actual ordinary acceptance reconciliation", e)
					}
					accepted, e := os.ReadFile(filepath.Join(destParent, "accepted/identity.key"))
					if e != nil || !bytes.Equal(accepted, files["identity.key"]) {
						t.Fatal("destination recovery changed key", e)
					}
				}
				for name, want := range files {
					got, e := os.ReadFile(filepath.Join(dir, name))
					if e != nil || !bytes.Equal(want, got) {
						t.Fatal("saved prefix changed", stage, name, e)
					}
				}
				for name, raw := range files {
					if bytes.Contains(raw, []byte(code)) {
						t.Fatal("claim code persisted", name)
					}
				}
			})
		}
		t.Log("PASS actual installer-bound process exits at ten retained protocol boundaries including partial/install plan writes, product refuses all create replay, only explicit same-key complete-state post-commit recovery completes; partial result refuses unchanged; no powercut/filesystem claim")
	}
	t.Log("PASS actual product normal new claim commits once and retains verified response, safe summary/no install/start/clear")
	t.Log("PASS actual product repeat recovery checks current server revocation despite cached verified result, refusal preserves key/response")
	t.Log("PASS assembled product server service -> real post-commit socket loss -> actual edge-agent create and separate retained-key recovery during actual directory replacement -> actual product binary safe review, exact bytes/key retained only in pinned original directory and no install/start/clear permission")
}

// Disposable test-only row ownership, never product cleanup.
func seedCandidateFixtureClaim(t *testing.T, s *server, tenant, serial string, seed func(*testing.T, *server, string, string) (string, string)) (string, string) {
	t.Helper()
	var exists bool
	if err := s.st.Pool.QueryRow(t.Context(), `SELECT EXISTS(SELECT 1 FROM tenants WHERE id=$1) OR EXISTS(SELECT 1 FROM gateways WHERE serial=$2)`, tenant, serial).Scan(&exists); err != nil || exists {
		t.Fatal("candidate fixture ownership not fresh", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		tx, err := s.st.Pool.Begin(ctx)
		if err != nil {
			t.Error("candidate fixture cleanup begin", err)
			return
		}
		defer tx.Rollback(ctx)
		// No cascades: response -> token -> gateway -> site -> tenant.
		var schema bool
		if err = tx.QueryRow(ctx, `SELECT to_regclass('enrollment_attempt_candidate') IS NOT NULL`).Scan(&schema); err != nil {
			t.Error(err)
			return
		}
		if schema {
			_, err = tx.Exec(ctx, `DELETE FROM enrollment_attempt_candidate WHERE tenant_id=$1 AND serial=$2 AND gateway_id IN (SELECT id FROM gateways WHERE tenant_id=$1 AND serial=$2)`, tenant, serial)
			if err != nil {
				t.Error("candidate response cleanup", err)
				return
			}
		}
		for _, query := range []string{
			`DELETE FROM enrollment_tokens WHERE tenant_id=$1 AND serial=$2`,
			`DELETE FROM gateways WHERE tenant_id=$1 AND serial=$2`,
			`DELETE FROM sites WHERE tenant_id=$1 AND id=$1||'-site'`,
			`DELETE FROM tenants WHERE id=$1`,
		} {
			// Only statements using serial receive the second argument.
			args := []any{tenant}
			if strings.Contains(query, "$2") {
				args = append(args, serial)
			}
			if _, err = tx.Exec(ctx, query, args...); err != nil {
				t.Error("candidate fixture cleanup refused", err)
				return
			}
		}
		if err = tx.Commit(ctx); err != nil {
			t.Error("candidate fixture cleanup commit", err)
			return
		}
		if err = s.st.Pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM tenants WHERE id=$1) OR EXISTS(SELECT 1 FROM gateways WHERE serial=$2) OR EXISTS(SELECT 1 FROM enrollment_tokens WHERE tenant_id=$1) OR EXISTS(SELECT 1 FROM sites WHERE tenant_id=$1 AND id=$1||'-site')`, tenant, serial).Scan(&exists); err != nil || exists {
			t.Error("candidate fixture absence unverified", err)
		}
		if schema {
			if err = s.st.Pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM enrollment_attempt_candidate WHERE tenant_id=$1 AND serial=$2)`, tenant, serial).Scan(&exists); err != nil || exists {
				t.Error("candidate response absence unverified", err)
			}
		}
	})
	return seed(t, s, tenant, serial)
}

// A subprocess permits intentional t.Fatal without hiding a failed subtest.
func TestIntegrationCandidateFixtureFailureCleanup(t *testing.T) {
	s, _ := testServer(t)
	const tenant = "itest-candidate-cleanup-cut"
	const serial = "candidate-cleanup-cut-serial"
	mode := os.Getenv("HEX_CANDIDATE_FIXTURE_FAILURE_CUT")
	if mode != "" {
		seedCandidateFixtureClaim(t, s, tenant, serial, func(t *testing.T, s *server, tenant, serial string) (string, string) {
			if mode == "preexisting" {
				t.Fatal("unexpected seed reached existing fixture")
			}
			if _, err := s.st.Pool.Exec(t.Context(), `INSERT INTO tenants(id,name) VALUES($1,$1)`, tenant); err != nil {
				t.Fatal(err)
			}
			if _, err := s.st.Pool.Exec(t.Context(), `INSERT INTO sites(id,tenant_id,name) VALUES($1,$2,'partial')`, tenant+"-site", tenant); err != nil {
				t.Fatal(err)
			}
			t.Fatal("intentional partial seed failure")
			return "", ""
		})
		return
	}
	child := func(mode string) []byte {
		cmd := exec.Command(os.Args[0], "-test.run=^TestIntegrationCandidateFixtureFailureCleanup$", "-test.v")
		cmd.Env = append(os.Environ(), "HEX_CANDIDATE_FIXTURE_FAILURE_CUT="+mode)
		out, err := cmd.CombinedOutput()
		if err == nil {
			t.Fatal("failure-cut unexpectedly passed", string(out))
		}
		return out
	}
	var exists bool
	if err := s.st.Pool.QueryRow(t.Context(), `SELECT EXISTS(SELECT 1 FROM tenants WHERE id=$1) OR EXISTS(SELECT 1 FROM gateways WHERE serial=$2) OR EXISTS(SELECT 1 FROM sites WHERE id=$1||'-site')`, tenant, serial).Scan(&exists); err != nil || exists {
		t.Fatal("failure-cut not fresh", err)
	}
	if out := child("partial"); !bytes.Contains(out, []byte("intentional partial seed failure")) {
		t.Fatal("wrong failure", string(out))
	}
	if err := s.st.Pool.QueryRow(t.Context(), `SELECT EXISTS(SELECT 1 FROM tenants WHERE id=$1) OR EXISTS(SELECT 1 FROM sites WHERE id=$1||'-site')`, tenant).Scan(&exists); err != nil || exists {
		t.Fatal("partial seed cleanup failed", err)
	}
	t.Cleanup(func() {
		if _, err := s.st.Pool.Exec(context.Background(), `DELETE FROM tenants WHERE id=$1 AND name='preexisting sentinel'`, tenant); err != nil {
			t.Error(err)
		}
	})
	if _, err := s.st.Pool.Exec(t.Context(), `INSERT INTO tenants(id,name) VALUES($1,'preexisting sentinel')`, tenant); err != nil {
		t.Fatal(err)
	}
	if out := child("preexisting"); !bytes.Contains(out, []byte("candidate fixture ownership not fresh")) {
		t.Fatal("wrong refusal", string(out))
	}
	var name string
	if err := s.st.Pool.QueryRow(t.Context(), `SELECT name FROM tenants WHERE id=$1`, tenant).Scan(&name); err != nil || name != "preexisting sentinel" {
		t.Fatal("preexisting fixture changed", err)
	}
}

func TestIntegrationCommissioningSharedAuthRefusals(t *testing.T) {
	s, _ := testServer(t)
	s.secret = []byte("synthetic-auth-refusal-secret-42")
	tenant := "itest-commissioning-auth"
	ctx := t.Context()
	var exists bool
	if e := s.st.Pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM tenants WHERE id=$1)`, tenant).Scan(&exists); e != nil || exists {
		t.Fatal("fixture not fresh", e)
	}
	t.Cleanup(func() {
		for _, q := range []string{`DELETE FROM users WHERE tenant_id=$1`, `DELETE FROM sites WHERE tenant_id=$1`, `DELETE FROM tenants WHERE id=$1`} {
			if _, e := s.st.Pool.Exec(context.Background(), q, tenant); e != nil {
				t.Error(e)
			}
		}
	})
	for _, q := range []string{`INSERT INTO tenants(id,name)VALUES($1,$1)`, `INSERT INTO sites(id,tenant_id,name)VALUES($1||'-site',$1,'Owned')`, `INSERT INTO users(id,tenant_id,email,display_name,role)VALUES($1||'-user',$1,$1||'@example.invalid','Operator','operator')`} {
		if _, e := s.st.Pool.Exec(ctx, q, tenant); e != nil {
			t.Fatal(e)
		}
	}
	s.commissioning = &commissioningSetup{Origin: "https://example.invalid"}
	api, mux := http.NewServeMux(), http.NewServeMux()
	registerEnrollmentMint(api, s)
	registerAuthenticatedAPI(mux, api, s)
	tok := func(ten string) string {
		v, e := jwt.NewWithClaims(jwt.SigningMethodHS256, auth.Claims{TenantID: ten, Role: "operator", RegisteredClaims: jwt.RegisteredClaims{Subject: tenant + "-user", ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour))}}).SignedString(s.secret)
		if e != nil {
			t.Fatal(e)
		}
		return v
	}
	do := func(token, site string) int {
		r := httptest.NewRequest("POST", "https://example.invalid/v1/enrollment/tokens", strings.NewReader(fmt.Sprintf(`{"site_id":%q,"serial":"auth-refused-serial","kind":"edge","ttl_hours":1}`, site)))
		r.Header.Set("X-HexThings-Commissioning", "1")
		if token != "" {
			r.Header.Set("Authorization", "Bearer "+token)
		}
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		return w.Code
	}
	if c := do("", tenant+"-site"); c != 401 {
		t.Fatal("unauthenticated", c)
	}
	if c := do(tok("other-tenant"), tenant+"-site"); c != 403 {
		t.Fatal("wrong tenant", c)
	}
	if c := do(tok(tenant), "foreign-site"); c != 409 {
		t.Fatal("foreign site", c)
	}
	if _, e := s.st.Pool.Exec(ctx, `UPDATE users SET disabled_at=now()WHERE id=$1||'-user'`, tenant); e != nil {
		t.Fatal(e)
	}
	if c := do(tok(tenant), tenant+"-site"); c != 401 {
		t.Fatal("inactive", c)
	}
	if _, e := s.st.Pool.Exec(ctx, `UPDATE users SET disabled_at=NULL,role='viewer' WHERE id=$1||'-user'`, tenant); e != nil {
		t.Fatal(e)
	}
	if c := do(tok(tenant), tenant+"-site"); c != 403 {
		t.Fatal("insufficient role", c)
	}
	var n int
	if e := s.st.Pool.QueryRow(ctx, `SELECT count(*)FROM gateways WHERE serial='auth-refused-serial'`).Scan(&n); e != nil || n != 0 {
		t.Fatal("refusal wrote gateway", e, n)
	}
}

func TestIntegrationLegacyMintDefaultsUnchanged(t *testing.T) {
	s, _ := testServer(t)
	seedCandidateFixtureClaim(t, s, "itest-legacy-mint-policy", "legacy-mint-policy", func(t *testing.T, s *server, tenant, serial string) (string, string) {
		for _, q := range []string{`INSERT INTO tenants(id,name)VALUES($1,$1)`, `INSERT INTO sites(id,tenant_id,name)VALUES($1||'-site',$1,'Legacy')`} {
			if _, e := s.st.Pool.Exec(t.Context(), q, tenant); e != nil {
				t.Fatal(e)
			}
		}
		t.Cleanup(func() {
			if _, e := s.st.Pool.Exec(context.Background(), `DELETE FROM audit_log WHERE tenant_id=$1 AND action='enrollment.mint'`, tenant); e != nil {
				t.Error(e)
			}
		})
		mux := http.NewServeMux()
		registerEnrollmentMint(mux, s)
		w := call(mux, tenant, "operator", "POST", "/v1/enrollment/tokens", `{"site_id":"itest-legacy-mint-policy-site","serial":"legacy-mint-policy","kind":"edge"}`)
		if w.Code != 201 {
			t.Fatal(w.Code, w.Body.String())
		}
		var v struct {
			Expires time.Time `json:"expires_at"`
			Code    string    `json:"claim_code"`
			Gateway string    `json:"gateway_id"`
			Enroll  string    `json:"enroll_string"`
		}
		if json.Unmarshal(w.Body.Bytes(), &v) != nil || time.Until(v.Expires) < 71*time.Hour || time.Until(v.Expires) > 72*time.Hour || v.Enroll == "" {
			t.Fatal("legacy72h delivery changed")
		}
		return v.Code, v.Gateway
	})
}
