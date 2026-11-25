//go:build linux

package setup

import (
	"github.com/abhisekrath00-ux/iot-platform/edge/internal/claim"
	"os"
	"path/filepath"
	"testing"
)

func TestReadVerifiedHandoffExactStateIdentityAndBytes(t *testing.T) {
	for _, kind := range []string{"valid", "partial-receipt", "foreign-attempt", "changed-binary", "changed-key", "changed-target", "extra"} {
		t.Run(kind, func(t *testing.T) {
			p, s, _, _ := runtimeFixture(t)
			i, f := handoffFixtureData()
			if e := commitNamedRuntimeBundle(s, i, f, "handoff"); e != nil {
				t.Fatal(e)
			}
			held, e := os.Open(p)
			if e != nil {
				t.Fatal(e)
			}
			defer held.Close()
			v := claim.VerifiedPrivateRuntime{Attempt: i.Attempt, Files: map[string][]byte{}, Binary: f["agent"], Deployment: i.Deployment, StateHandle: held}
			for n := range i.Identity {
				v.Files[n] = f[n]
			}
			switch kind {
			case "partial-receipt":
				os.WriteFile(filepath.Join(p, "handoff.complete.json"), []byte("{"), 0600)
			case "foreign-attempt":
				v.Attempt = runtimeHash([]byte("other"))
			case "changed-binary":
				v.Binary = []byte("other")
			case "changed-key":
				v.Files["identity.key"] = []byte("other")
			case "changed-target":
				d := *v.Deployment
				d.Unit = "/other/unit"
				v.Deployment = &d
			case "extra":
				os.WriteFile(filepath.Join(p, "handoff/unknown"), []byte("x"), 0600)
			}
			before := runtimeSnapshot(t, p)
			_, files, e := readVerifiedDeploymentHandoff(v)
			if kind == "valid" {
				if e != nil || len(files) != 8 {
					t.Fatal(e)
				}
			} else if e == nil {
				t.Fatal("mismatch accepted")
			}
			if string(mustJSON(before)) != string(mustJSON(runtimeSnapshot(t, p))) {
				t.Fatal("handoff read wrote")
			}
		})
	}
}
