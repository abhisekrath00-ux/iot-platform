//go:build linux

package setup

import (
	"bytes"
	"encoding/json"
	"fmt"
	"golang.org/x/sys/unix"
	"os"
)

func (t *deploymentTransaction) unitIntent() []byte {
	raw, _ := json.Marshal(struct {
		Version int
		Phase   string
		Payload runtimeBundleIntent
	}{1, "stopped_unit_last", t.intent})
	return raw
}
func (t *deploymentTransaction) inspectStoppedUnit() (string, error) {
	p := t.parents["deployment_unit_parent"]
	leaf := t.leaves["deployment_unit_parent"]
	raw := t.unitIntent()
	old, e := p.read(leaf+".unit.intent.json", 131072)
	pending := !p.absent(leaf + ".unit.pending")
	final := !p.absent(leaf)
	receipt := !p.absent(leaf + ".unit.complete.json")
	if os.IsNotExist(e) {
		if pending || final || receipt {
			return "", fmt.Errorf("unbound stopped unit")
		}
		return "unit_unstarted", nil
	}
	if e != nil || !bytes.Equal(old, raw) || pending && final || receipt && !final {
		return "", fmt.Errorf("partial or inconsistent unit intent")
	}
	if !pending && !final {
		return "unit_intent_only", nil
	}
	name := leaf + ".unit.pending"
	if final {
		name = leaf
	}
	b, e := p.read(name, int64(len(t.files["unit.service"])))
	if e != nil || !bytes.Equal(b, t.files["unit.service"]) {
		return "", fmt.Errorf("partial or foreign stopped unit")
	}
	if receipt {
		b, e = p.read(leaf+".unit.complete.json", 131072)
		if e != nil || !bytes.Equal(b, raw) {
			return "", fmt.Errorf("unit receipt partial or differs")
		}
		return "unit_complete_stopped", nil
	}
	if final {
		return "unit_promoted_no_receipt", nil
	}
	return "unit_complete_pending", nil
}

// Caller holds the same install lock as access/state; this never reloads,
// starts or enables. Root-readable0600 unit is installed only after all data.
func (t *deploymentTransaction) commitStoppedUnit(uid uint32) error {
	if e := t.validateParents(uid); e != nil {
		return e
	}
	p := t.parents["deployment_unit_parent"]
	leaf := t.leaves["deployment_unit_parent"]
	raw := t.unitIntent()
	state, e := t.inspectStoppedUnit()
	if e != nil {
		return e
	}
	if state == "unit_unstarted" {
		if e = p.write(leaf+".unit.intent.json", raw); e != nil {
			return e
		}
		runtimeBundleCheckpoint("unit_after_intent")
	}
	if p.absent(leaf) && p.absent(leaf+".unit.pending") {
		if e = p.write(leaf+".unit.pending", t.files["unit.service"]); e != nil {
			return e
		}
		runtimeBundleCheckpoint("unit_after_file")
	}
	if e = t.validateParents(uid); e != nil {
		return e
	}
	if p.absent(leaf) {
		if e = unix.Renameat2(p.fd, leaf+".unit.pending", p.fd, leaf, unix.RENAME_NOREPLACE); e != nil {
			return e
		}
		if e = unix.Fsync(p.fd); e != nil {
			return e
		}
		runtimeBundleCheckpoint("unit_after_promotion")
	}
	if p.absent(leaf + ".unit.complete.json") {
		if e = p.write(leaf+".unit.complete.json", raw); e != nil {
			return e
		}
		runtimeBundleCheckpoint("unit_after_receipt")
	}
	_, e = t.inspectStoppedUnit()
	return e
}
