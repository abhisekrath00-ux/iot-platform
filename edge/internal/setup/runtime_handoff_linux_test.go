//go:build linux

package setup

import (
	"github.com/abhisekrath00-ux/iot-platform/edge/internal/claim"
	"os"
	"path/filepath"
	"testing"
)

func handoffFixtureData() (runtimeBundleIntent, map[string][]byte) {
	i, f := runtimeFixtureData()
	i.Version = 2
	i.Deployment = &claim.VerifiedDeployment{Root: "/explicit/payload", State: "/explicit/state", Unit: "/explicit/unit", ServiceUID: "10001", ServiceGID: "10002", Activation: "stopped-only", ParentSnapshot: map[string]string{"deployment_parent": "1:2", "deployment_state_parent": "1:3", "deployment_unit_parent": "1:4"}}
	for _, n := range []string{"broker-ca.pem", "device-ca.pem", "identity.json", "identity.crt", "identity.key"} {
		f[n] = []byte(n)
		i.Files[n] = runtimeHash(f[n])
	}
	return i, f
}
func TestHandoffCompletePrivateExactBindings(t *testing.T) {
	p, s, _, _ := runtimeFixture(t)
	i, f := handoffFixtureData()
	if e := commitNamedRuntimeBundle(s, i, f, "handoff"); e != nil {
		t.Fatal(e)
	}
	if e := commitNamedRuntimeBundle(s, i, f, "handoff"); e != nil {
		t.Fatal("exact repeat", e)
	}
	for n := range f {
		st, e := os.Stat(filepath.Join(p, "handoff", n))
		if e != nil || st.Mode().Perm() != 0600 {
			t.Fatal("nonexec complete file", n, e)
		}
	}
	for _, kind := range []string{"uid", "gid", "activation", "path", "parents", "identity", "extra", "missing"} {
		t.Run(kind, func(t *testing.T) {
			_, s, _, _ := runtimeFixture(t)
			i, f := handoffFixtureData()
			switch kind {
			case "uid":
				i.Deployment.ServiceUID = "0"
			case "gid":
				i.Deployment.ServiceGID = "+1"
			case "activation":
				i.Deployment.Activation = "start"
			case "path":
				i.Deployment.Root = ""
			case "parents":
				delete(i.Deployment.ParentSnapshot, "deployment_parent")
			case "identity":
				f["identity.key"] = []byte("other")
				i.Files["identity.key"] = runtimeHash(f["identity.key"])
			case "extra":
				f["unknown"] = []byte("x")
			case "missing":
				delete(f, "broker-ca.pem")
			}
			if commitNamedRuntimeBundle(s, i, f, "handoff") == nil {
				t.Fatal("invalid handoff committed")
			}
		})
	}
	i.Deployment.Unit = "/different/unit"
	if commitNamedRuntimeBundle(s, i, f, "handoff") == nil {
		t.Fatal("changed final binding accepted")
	}
}
