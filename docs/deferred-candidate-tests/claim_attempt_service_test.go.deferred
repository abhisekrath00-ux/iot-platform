package main

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/ed25519"
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
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/abhisekrath00-ux/iot-platform/server/internal/enroll"
	"github.com/abhisekrath00-ux/iot-platform/server/internal/pki"
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
	cfg := enroll.CandidateServiceConfig{Enabled: true, Pool: s.st.Pool, Issuer: enroll.CandidateIssuer{CA: ca, CAKey: cak, ResponseKey: key, TokenKey: []byte("synthetic-service-hmac-key-not-owner-data"), Lifetime: 40 * time.Minute}}
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
	codeBad, gwBad := seedClaim(t, s, "itest-issued-refusal", "issued-refusal-serial")
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
	code, _ := seedClaim(t, s, "itest-candidate-service", "service-serial")
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
	code2, gw2 := seedClaim(t, s, "itest-service-process", "service-process-serial")
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
	code3, gw3 := seedClaim(t, s, "itest-product-normal-create", "normal-product-serial")
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
	targets["DeploymentRoot"] = filepath.Join(finalParent, "payload")
	targets["DeploymentState"] = filepath.Join(finalParent, "state")
	targets["DeploymentUnit"] = filepath.Join(finalParent, "hex.service")
	targets["ServiceUID"], targets["ServiceGID"], targets["Activation"] = "10001", "10002", "stopped-only"
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
	var boundRequests atomic.Int32
	var changeTargetDuringReply atomic.Bool
	boundServer := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		boundRequests.Add(1)
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
	defer boundServer.Close()
	boundTransport := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: boundServer.Certificate().Raw})
	boundCode, _ := seedClaim(t, s, "itest-bound-normal", "bound-normal-serial")
	boundDir := filepath.Join(parent, "normal-bound-attempt")
	boundPayload, _ := json.Marshal(map[string]any{"Dir": boundDir, "Server": boundServer.URL, "Code": boundCode, "Serial": "bound-normal-serial", "TransportCA": string(boundTransport), "DeviceCA": string(ca), "ExpectedTenant": "itest-bound-normal", "ResponseKey": key.Public(), "Targets": targets})
	boundChild := productCommand(agent, "-create-staged-install")
	boundChild.Stdin = bytes.NewReader(boundPayload)
	boundOut, e := boundChild.CombinedOutput()
	var boundSummary struct {
		State   string `json:"state"`
		Install bool   `json:"can_install"`
		Start   bool   `json:"can_start"`
		Clear   bool   `json:"can_clear_pending"`
	}
	if e != nil || json.Unmarshal(boundOut, &boundSummary) != nil || boundSummary.State != "verified_install_attempt_retained" || boundSummary.Install || boundSummary.Start || boundSummary.Clear || !claimRedeemed(t, s, boundCode) {
		t.Fatal("normal bound staging", e, string(boundOut))
	}
	for _, secret := range []string{boundCode, "PRIVATE KEY", "ingest_token", "client_cert_pem", "csr_pem", boundDir} {
		if bytes.Contains(boundOut, []byte(secret)) {
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
	acceptOut, e := acceptChild.CombinedOutput()
	var acceptSummary struct {
		State   string `json:"state"`
		Install bool   `json:"can_install"`
		Start   bool   `json:"can_start"`
		Clear   bool   `json:"can_clear_pending"`
	}
	if e != nil || json.Unmarshal(acceptOut, &acceptSummary) != nil || acceptSummary.State != "private_identity_accepted_service_blocked" || acceptSummary.Install || acceptSummary.Start || acceptSummary.Clear {
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
		if bytes.Contains(acceptOut, []byte(secret)) {
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
	acceptedRuntimeBrokerSourceProof(t, s, agent, filepath.Join(acceptParent, "accepted"), mappedIdentity.Gateway, "itest-bound-normal", "bound-normal-serial", ca, cak, func(raw []byte) ([]byte, error) {
		child := productCommand(agent, "-review-runtime-mapping")
		child.Stdin = bytes.NewReader(raw)
		return child.CombinedOutput()
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
				code, _ := seedClaim(t, s, tenant, serial)
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
