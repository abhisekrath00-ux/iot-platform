//go:build linux

package setup

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestActivationAtMostOnceIndependentReceiptAndMutableState(t *testing.T) {
	v, i, f := deploymentFixture(t)
	uid, gid := uint32(os.Getuid()), uint32(os.Getgid())
	e := withDeploymentTransaction(v, i, f, uid, func(tr *deploymentTransaction) error {
		if e := tr.commitStoppedDeployment(uid, uid, gid); e != nil {
			return e
		}
		a, e := tr.activationExpected("tenant", "gateway", "device", "point", 110, time.Now())
		if e != nil {
			return e
		}
		starts := 0
		start := func() error {
			starts++
			return os.WriteFile(filepath.Join(v.Deployment.State, "queue.db"), []byte("runtime-data"), 0600)
		}
		if e = tr.startActivation(uid, uid, gid, a, start); e != nil {
			t.Fatal(e)
		}
		if starts != 1 {
			t.Fatal(starts)
		}
		if e = tr.startActivation(uid, uid, gid, a, start); e == nil || starts != 1 {
			t.Fatal("replayed uncertain start", e, starts)
		}
		if _, e = tr.inspectStoppedDeployment(uid, gid); e == nil {
			t.Fatal("stopped installer accepted activation marker")
		}
		good := deploymentMeasurement{EventID: "new-event", Tenant: "tenant", Gateway: "gateway", Device: "device", Point: "point", Quality: "measured", Value: 110, ReceivedUnixNano: a.BeganUnixNano + 1}
		for _, kind := range []string{"inactive", "old", "wrongpoint", "quality", "value", "error"} {
			m := good
			active := true
			var err error
			switch kind {
			case "inactive":
				active = false
			case "old":
				m.ReceivedUnixNano = a.BeganUnixNano - 1
			case "wrongpoint":
				m.Point = "other"
			case "quality":
				m.Quality = "cached"
			case "value":
				m.Value = 111
			case "error":
				err = os.ErrPermission
			}
			if e = tr.completeActivation(uid, uid, gid, a, func() (bool, deploymentMeasurement, error) { return active, m, err }); e == nil {
				t.Fatal("unverified health accepted", kind)
			}
		}
		for n := 0; n < 2; n++ {
			if e = tr.completeActivation(uid, uid, gid, a, func() (bool, deploymentMeasurement, error) { return true, good, nil }); e != nil {
				t.Fatal("exact live confirmation", e)
			}
		}
		phase, e := tr.inspectActivation(a)
		if e != nil || phase != "activation_measured_receipt" {
			t.Fatal(phase, e)
		}
		return nil
	})
	if e != nil {
		t.Fatal(e)
	}
}
func TestActivationRefusesChangedBindingsAndPartialMarkers(t *testing.T) {
	for _, kind := range []string{"partial-intent", "partial-start", "partial-receipt", "foreign-state", "revoked", "start-error"} {
		t.Run(kind, func(t *testing.T) {
			v, i, f := deploymentFixture(t)
			uid, gid := uint32(os.Getuid()), uint32(os.Getgid())
			e := withDeploymentTransaction(v, i, f, uid, func(tr *deploymentTransaction) error {
				if e := tr.commitStoppedDeployment(uid, uid, gid); e != nil {
					return e
				}
				a, e := tr.activationExpected("tenant", "gw", "dev", "point", 110, time.Now())
				if e != nil {
					return e
				}
				calls := 0
				if kind == "revoked" {
					tr.validate = func() error { return os.ErrPermission }
					if e = tr.startActivation(uid, uid, gid, a, func() error { calls++; return nil }); e == nil || calls != 0 {
						t.Fatal("revoked activation called start")
					}
					return nil
				}
				if kind == "start-error" {
					if e = tr.startActivation(uid, uid, gid, a, func() error { calls++; return os.ErrPermission }); e == nil {
						t.Fatal("error hidden")
					}
					if e = tr.startActivation(uid, uid, gid, a, func() error { calls++; return nil }); e == nil || calls != 1 {
						t.Fatal("error caused retry")
					}
					return nil
				}
				if e = tr.startActivation(uid, uid, gid, a, func() error { return nil }); e != nil {
					return e
				}
				switch kind {
				case "partial-intent":
					os.WriteFile(v.Deployment.Root+".activation.intent.json", []byte("{"), 0600)
				case "partial-start":
					os.WriteFile(v.Deployment.Root+".activation.start.json", []byte("{"), 0600)
				case "partial-receipt":
					os.WriteFile(v.Deployment.Root+".activation.complete.json", []byte("{"), 0600)
				case "foreign-state":
					os.WriteFile(v.Deployment.State+".state.inode.json", []byte("other"), 0600)
				}
				before := runtimeSnapshot(t, filepath.Dir(v.Deployment.Root))
				if _, e = tr.inspectActivation(a); e == nil {
					t.Fatal("unknown activation accepted")
				}
				if string(mustJSON(before)) != string(mustJSON(runtimeSnapshot(t, filepath.Dir(v.Deployment.Root)))) {
					t.Fatal("inspection wrote")
				}
				return nil
			})
			if e != nil {
				t.Fatal(e)
			}
		})
	}
}
