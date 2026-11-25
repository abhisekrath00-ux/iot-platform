//go:build linux

package setup

// Dormant activation protocol. No systemd or broker implementation lives here.
// A lost start result is uncertain and never blindly reissued. Caller supplies
// current authorization, live status and independent measured-source evidence.
import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"time"
)

type deploymentActivation struct {
	Version                        int
	Payload                        runtimeBundleIntent
	StateInode                     string
	BeganUnixNano                  int64
	Tenant, Gateway, Device, Point string
	Value                          float64
}
type deploymentMeasurement struct {
	EventID, Tenant, Gateway, Device, Point, Quality string
	Value                                            float64
	ReceivedUnixNano                                 int64
}

func (a deploymentActivation) valid() bool {
	return a.Version == 1 && a.BeganUnixNano > 0 && a.StateInode != "" && a.Tenant != "" && a.Gateway != "" && a.Device != "" && a.Point != "" && !math.IsNaN(a.Value) && !math.IsInf(a.Value, 0) && a.Payload.Version == 2 && runtimeValidHash(a.Payload.Attempt)
}
func (a deploymentActivation) accepts(m deploymentMeasurement) bool {
	return m.EventID != "" && m.Tenant == a.Tenant && m.Gateway == a.Gateway && m.Device == a.Device && m.Point == a.Point && m.Quality == "measured" && m.Value == a.Value && m.ReceivedUnixNano >= a.BeganUnixNano
}
func (t *deploymentTransaction) verifyActivationInstalled(serviceUID, serviceGID uint32) error {
	access, e := t.inspectStoppedAccessLater(serviceGID, true, true)
	if e != nil || access != "complete_stopped_service_access" {
		return fmt.Errorf("complete exact installed access required")
	}
	state, e := t.inspectWritableStateMode(serviceUID, serviceGID, true)
	if e != nil || state != "state_complete_empty" {
		return fmt.Errorf("exact installed state inode required")
	}
	unit, e := t.inspectStoppedUnit()
	if e != nil || unit != "unit_complete_stopped" {
		return fmt.Errorf("complete exact installed unit required")
	}
	return nil
}
func (t *deploymentTransaction) activationExpected(tenant, gateway, device, point string, value float64, now time.Time) (deploymentActivation, error) {
	p := t.parents["deployment_state_parent"]
	leaf := t.leaves["deployment_state_parent"]
	inode, e := p.read(leaf+".state.inode.json", 131072)
	if e != nil {
		return deploymentActivation{}, e
	}
	a := deploymentActivation{Version: 1, Payload: t.intent, StateInode: string(inode), BeganUnixNano: now.UnixNano(), Tenant: tenant, Gateway: gateway, Device: device, Point: point, Value: value}
	if !a.valid() {
		return a, fmt.Errorf("explicit measured-source activation target required")
	}
	return a, nil
}
func (t *deploymentTransaction) inspectActivation(a deploymentActivation) (string, error) {
	if !a.valid() {
		return "", fmt.Errorf("valid activation binding required")
	}
	x, _ := json.Marshal(a.Payload)
	y, _ := json.Marshal(t.intent)
	if !bytes.Equal(x, y) {
		return "", fmt.Errorf("activation payload differs")
	}
	p := t.parents["deployment_parent"]
	leaf := t.leaves["deployment_parent"]
	raw, _ := json.Marshal(a)
	b, e := p.read(leaf+".activation.intent.json", 131072)
	started := !p.absent(leaf + ".activation.start.json")
	receipt := !p.absent(leaf + ".activation.complete.json")
	if os.IsNotExist(e) {
		if started || receipt {
			return "", fmt.Errorf("unbound activation phase")
		}
		return "activation_unstarted", nil
	}
	if e != nil || !bytes.Equal(b, raw) || receipt && !started {
		return "", fmt.Errorf("activation intent partial or differs")
	}
	// Same inode binding is required even after runtime writes legitimate data.
	sp := t.parents["deployment_state_parent"]
	inode, e := sp.read(t.leaves["deployment_state_parent"]+".state.inode.json", 131072)
	if e != nil || string(inode) != a.StateInode {
		return "", fmt.Errorf("activation state binding differs")
	}
	if !started {
		return "activation_prepared_no_start", nil
	}
	b, e = p.read(leaf+".activation.start.json", 131072)
	if e != nil || !bytes.Equal(b, raw) {
		return "", fmt.Errorf("activation start marker partial or differs")
	}
	if !receipt {
		return "activation_start_uncertain", nil
	}
	b, e = p.read(leaf+".activation.complete.json", 131072)
	if e != nil {
		return "", e
	}
	var complete struct {
		Activation  deploymentActivation
		Measurement deploymentMeasurement
	}
	if decodeMappingInput(b, &complete) != nil {
		return "", fmt.Errorf("activation receipt partial")
	}
	x, _ = json.Marshal(complete.Activation)
	if !bytes.Equal(x, raw) || !a.accepts(complete.Measurement) {
		return "", fmt.Errorf("activation receipt binding differs")
	}
	return "activation_measured_receipt", nil
}

// start is called at most once after durable uncertain marker. It must perform
// service-user lint and exact live unit checks before actual start. This API is
// private and has no caller in the product. Live active state alone is not health.
func (t *deploymentTransaction) startActivation(uid, serviceUID, serviceGID uint32, a deploymentActivation, start func() error) error {
	if start == nil || !a.valid() {
		return fmt.Errorf("explicit validated activation required")
	}
	return t.withInstallLock(uid, func() error {
		if e := t.validateParents(uid); e != nil {
			return e
		}
		phase, e := t.inspectActivation(a)
		if e != nil {
			return e
		}
		if phase != "activation_unstarted" && phase != "activation_prepared_no_start" {
			return fmt.Errorf("start already attempted; reconcile live state, never replay")
		}
		if phase == "activation_unstarted" {
			if _, e = t.inspectStoppedDeployment(serviceUID, serviceGID); e != nil {
				return e
			}
		}
		if e = t.verifyActivationInstalled(serviceUID, serviceGID); e != nil {
			return e
		}
		// No mutable state is allowed before first start, including prepared intent.
		empty, e := t.inspectWritableState(serviceUID, serviceGID)
		if e != nil || empty != "state_complete_empty" {
			return fmt.Errorf("prestart state must be empty")
		}
		p := t.parents["deployment_parent"]
		leaf := t.leaves["deployment_parent"]
		raw, _ := json.Marshal(a)
		if phase == "activation_unstarted" {
			if e = p.write(leaf+".activation.intent.json", raw); e != nil {
				return e
			}
			runtimeBundleCheckpoint("activation_after_intent")
		}
		if e = t.validateParents(uid); e != nil {
			return e
		}
		if e = p.write(leaf+".activation.start.json", raw); e != nil {
			return e
		}
		runtimeBundleCheckpoint("activation_before_start")
		if e = start(); e != nil {
			return fmt.Errorf("activation result uncertain; preserve marker and reconcile live service")
		}
		runtimeBundleCheckpoint("activation_after_start")
		return nil
	})
}

// completeActivation never starts/restarts a service. Both live exact service
// state and independent source/ingest receipt must be checked on this attempt.
func (t *deploymentTransaction) completeActivation(uid, serviceUID, serviceGID uint32, a deploymentActivation, observe func() (bool, deploymentMeasurement, error)) error {
	if observe == nil {
		return fmt.Errorf("independent live observer required")
	}
	return t.withInstallLock(uid, func() error {
		if e := t.validateParents(uid); e != nil {
			return e
		}
		if e := t.verifyActivationInstalled(serviceUID, serviceGID); e != nil {
			return e
		}
		phase, e := t.inspectActivation(a)
		if e != nil {
			return e
		}
		if phase != "activation_start_uncertain" && phase != "activation_measured_receipt" {
			return fmt.Errorf("same-attempt start marker required")
		}
		active, m, e := observe()
		if e != nil || !active || !a.accepts(m) {
			return fmt.Errorf("live exact service and independent measured-source receipt not established")
		}
		if phase == "activation_measured_receipt" {
			return nil
		}
		raw, _ := json.Marshal(struct {
			Activation  deploymentActivation
			Measurement deploymentMeasurement
		}{a, m})
		p := t.parents["deployment_parent"]
		leaf := t.leaves["deployment_parent"]
		if e = p.write(leaf+".activation.complete.json", raw); e != nil {
			return e
		}
		runtimeBundleCheckpoint("activation_after_receipt")
		return nil
	})
}

// Recovery uses the original durable binding. It never invents a new timestamp
// or target from an uncertain start, and does not contact/restart a service.
func (t *deploymentTransaction) loadActivation() (deploymentActivation, string, error) {
	p := t.parents["deployment_parent"]
	leaf := t.leaves["deployment_parent"]
	raw, e := p.read(leaf+".activation.intent.json", 131072)
	if e != nil {
		return deploymentActivation{}, "", e
	}
	a := deploymentActivation{Payload: runtimeBundleIntent{Deployment: t.intent.Deployment}}
	if decodeMappingInput(raw, &a) != nil || !a.valid() {
		return a, "", fmt.Errorf("activation intent partial or invalid")
	}
	phase, e := t.inspectActivation(a)
	return a, phase, e
}
