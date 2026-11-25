//go:build !windows

package claim

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func deploymentFixture(t *testing.T) (string, candidateInstallTargets) {
	t.Helper()
	root := t.TempDir()
	if e := os.Chmod(root, 0700); e != nil {
		t.Fatal(e)
	}
	v := candidateInstallTargets{Binary: filepath.Join(root, "bin/agent"), Config: filepath.Join(root, "etc/config"), State: filepath.Join(root, "state"), Unit: filepath.Join(root, "units/review"), Destination: filepath.Join(root, "accepted"), DeploymentRoot: filepath.Join(root, "final/payload"), DeploymentState: filepath.Join(root, "data/state"), DeploymentUnit: filepath.Join(root, "systemd/hex.service"), ServiceUID: "10001", ServiceGID: "10002", Activation: "stopped-only"}
	for _, p := range []string{v.Binary, v.Config, v.Unit} {
		if e := os.MkdirAll(filepath.Dir(p), 0700); e != nil {
			t.Fatal(e)
		}
		if e := os.WriteFile(p, []byte("synthetic"), 0600); e != nil {
			t.Fatal(e)
		}
	}
	for _, p := range []string{v.State, filepath.Dir(v.DeploymentRoot), filepath.Dir(v.DeploymentState), filepath.Dir(v.DeploymentUnit)} {
		if e := os.MkdirAll(p, 0700); e != nil {
			t.Fatal(e)
		}
	}
	return filepath.Join(root, "attempt"), v
}

func TestDeploymentContractCanonicalCompleteAndStopped(t *testing.T) {
	dir, v := deploymentFixture(t)
	if !candidateValidInstallTargets(dir, v) {
		t.Fatal("complete contract refused")
	}
	for _, kind := range []string{"partial", "rootuid", "leadingzero", "signed", "overflow", "activation", "overlap", "stage", "same", "unclean"} {
		t.Run(kind, func(t *testing.T) {
			w := v
			switch kind {
			case "partial":
				w.DeploymentState = ""
			case "rootuid":
				w.ServiceUID = "0"
			case "leadingzero":
				w.ServiceGID = "010"
			case "signed":
				w.ServiceGID = "+10"
			case "overflow":
				w.ServiceUID = "4294967295"
			case "activation":
				w.Activation = "start"
			case "overlap":
				w.DeploymentRoot = filepath.Dir(w.Binary)
			case "stage":
				w.DeploymentRoot = filepath.Join(dir, "payload")
			case "same":
				w.DeploymentUnit = w.DeploymentRoot
			case "unclean":
				w.DeploymentRoot += "/../payload"
			}
			if candidateValidInstallTargets(dir, w) {
				t.Fatal("invalid contract accepted")
			}
		})
	}
	for _, raw := range []string{`{"Targets":{"ServiceUID":"1","ServiceUID":"2"}}`, `{"Targets":{"Activation":"stopped-only","Unknown":1}}`} {
		if candidateStrictInstallTargets([]byte(raw)) {
			t.Fatal("ambiguous contract accepted")
		}
	}
}

func TestDeploymentSnapshotRefusesExistingLeavesAndChangedParent(t *testing.T) {
	dir, v := deploymentFixture(t)
	if !candidateValidInstallTargets(dir, v) {
		t.Fatal("fixture")
	}
	baseline, e := candidateSnapshotInstallTargets(v)
	if e != nil || len(baseline) != 10 {
		t.Fatal(e, baseline)
	}
	plan := candidateInstallPlan{Targets: v, TargetSnapshot: baseline}
	g, e := candidateHoldInstallTargets(plan)
	if e != nil {
		t.Fatal(e)
	}
	defer g.Close()
	for _, p := range []string{v.DeploymentRoot, v.DeploymentState, v.DeploymentUnit} {
		for _, kind := range []string{"file", "dir", "symlink"} {
			switch kind {
			case "file":
				e = os.WriteFile(p, []byte("existing"), 0600)
			case "dir":
				e = os.Mkdir(p, 0700)
			case "symlink":
				e = os.Symlink("absent", p)
			}
			if e != nil {
				t.Fatal(e)
			}
			if candidateCheckInstallSnapshot(plan) == nil || g.Validate() == nil {
				t.Fatal("occupied final target accepted", kind)
			}
			if e = os.Remove(p); e != nil {
				t.Fatal(e)
			}
		}
	}
	parent := filepath.Dir(v.DeploymentRoot)
	if e = os.Rename(parent, parent+"-old"); e != nil {
		t.Fatal(e)
	}
	if e = os.Mkdir(parent, 0700); e != nil {
		t.Fatal(e)
	}
	if g.Validate() == nil {
		t.Fatal("replaced empty deployment parent accepted")
	}
	if e = os.Remove(parent); e != nil {
		t.Fatal(e)
	}
	if e = os.Rename(parent+"-old", parent); e != nil {
		t.Fatal(e)
	}
	if e = g.Validate(); e != nil {
		t.Fatal("restored parent refused", e)
	}
	after, e := candidateSnapshotInstallTargets(v)
	if e != nil || !reflect.DeepEqual(after, baseline) {
		t.Fatal("refusals changed baseline", e)
	}
}

func TestDeploymentContractDurableBeforeHTTPAndCheckedOnRecovery(t *testing.T) {
	dir, v := deploymentFixture(t)
	var calls int
	var plan candidateInstallPlan
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		b, e := os.ReadFile(filepath.Join(dir, "candidate-install-plan.json"))
		if e != nil || candidateDecodeRecovery(b, &plan) != nil || plan.Targets != v || len(plan.TargetSnapshot) != 10 {
			t.Error("HTTP preceded exact durable deployment contract", e)
		}
		w.WriteHeader(403)
	}))
	defer srv.Close()
	ca := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: srv.Certificate().Raw})
	pub, _, _ := ed25519.GenerateKey(rand.Reader)
	in := map[string]any{"Dir": dir, "Server": srv.URL, "Serial": "SER", "Code": "synthetic", "TransportCA": string(ca), "DeviceCA": string(ca), "ExpectedTenant": "tenant", "ResponseKey": pub, "Targets": v}
	raw, _ := json.Marshal(in)
	var out bytes.Buffer
	if RunCandidateInstallStage(t.Context(), bytes.NewReader(raw), &out) == nil || calls != 1 {
		t.Fatal("expected denied first request", calls)
	}
	key, e := os.ReadFile(filepath.Join(dir, "identity.key"))
	if e != nil {
		t.Fatal(e)
	}
	delete(in, "Targets")
	recoverRaw, _ := json.Marshal(in)
	if e = os.WriteFile(v.DeploymentUnit, []byte("conflict"), 0600); e != nil {
		t.Fatal(e)
	}
	if RunCandidateRecovery(t.Context(), bytes.NewReader(recoverRaw), &out) == nil || calls != 1 {
		t.Fatal("changed final target reached recovery HTTP", calls)
	}
	if e = os.Remove(v.DeploymentUnit); e != nil {
		t.Fatal(e)
	}
	if RunCandidateRecovery(t.Context(), bytes.NewReader(recoverRaw), &out) == nil || calls != 2 {
		t.Fatal("unchanged contract did not reach recovery", calls)
	}
	after, e := os.ReadFile(filepath.Join(dir, "identity.key"))
	if e != nil || !bytes.Equal(after, key) {
		t.Fatal("identity changed", e)
	}
	for _, p := range []string{v.DeploymentRoot, v.DeploymentState, v.DeploymentUnit} {
		if _, e = os.Lstat(p); !os.IsNotExist(e) {
			t.Fatal("binding performed installation", p, e)
		}
	}
}
