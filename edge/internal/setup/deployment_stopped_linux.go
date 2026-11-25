//go:build linux

package setup

import (
	"fmt"
	"strings"
)

// Stopped composition permits only exact bound writable-state progress after
// complete payload access. A later state never relaxes earlier prerequisites.
func (t *deploymentTransaction) inspectStoppedDeployment(serviceUID, serviceGID uint32) (string, error) {
	p := t.parents["deployment_parent"]
	leaf := t.leaves["deployment_parent"]
	for _, suffix := range []string{".activation.intent.json", ".activation.start.json", ".activation.complete.json"} {
		if !p.absent(leaf + suffix) {
			return "", fmt.Errorf("activation may have begun; stopped install recovery refused")
		}
	}
	access, e := t.inspectStoppedAccessLater(serviceGID, true, true)
	if e != nil {
		return "", e
	}
	state, e := t.inspectWritableState(serviceUID, serviceGID)
	if e != nil {
		return "", e
	}
	if state != "state_unstarted" && access != "complete_stopped_service_access" {
		return "", fmt.Errorf("writable state preceded complete service access")
	}
	unit, e := t.inspectStoppedUnit()
	if e != nil {
		return "", e
	}
	if unit != "unit_unstarted" && state != "state_complete_empty" {
		return "", fmt.Errorf("unit preceded complete bound state")
	}
	if unit != "unit_unstarted" {
		return unit, nil
	}
	if state == "state_unstarted" {
		return access, nil
	}
	return "stopped_" + state, nil
}
func (t *deploymentTransaction) commitStoppedDeployment(uid, serviceUID, serviceGID uint32) error {
	state, e := t.inspectStoppedDeployment(serviceUID, serviceGID)
	if e != nil {
		return e
	}
	if !strings.HasPrefix(state, "access_prefix_") && !strings.HasPrefix(state, "stopped_state_") && !strings.HasPrefix(state, "unit_") && state != "complete_stopped_service_access" {
		if e = t.commit(uid); e != nil {
			return e
		}
	}
	return t.withInstallLock(uid, func() error {
		if e := t.validateParents(uid); e != nil {
			return e
		}
		access, e := t.inspectStoppedAccessLater(serviceGID, true, true)
		if e != nil {
			return e
		}
		if access != "complete_stopped_service_access" {
			if e = t.commitStoppedAccessLocked(uid, serviceGID); e != nil {
				return e
			}
		}
		if e = t.commitWritableState(uid, serviceUID, serviceGID); e != nil {
			return e
		}
		if e = t.commitStoppedUnit(uid); e != nil {
			return e
		}
		_, e = t.inspectStoppedDeployment(serviceUID, serviceGID)
		return e
	})
}
