//go:build windows

package claim

import "fmt"

func candidateSnapshotInstallTargets(candidateInstallTargets) (map[string]string, error) {
	return nil, fmt.Errorf("native Windows installed target inspection unavailable; installation remains refused")
}

type candidateInstallTargetGuard struct{}

func candidateHoldInstallTargets(candidateInstallPlan) (*candidateInstallTargetGuard, error) {
	return nil, fmt.Errorf("native Windows installed target guard unavailable")
}
func (*candidateInstallTargetGuard) Close() {}
func (*candidateInstallTargetGuard) Validate() error {
	return fmt.Errorf("native Windows installed target guard unavailable")
}

func candidateRequireNewDestination(candidateInstallTargets) error {
	return fmt.Errorf("native destination preflight unavailable")
}

func candidateHoldInstallTargetsChecked(candidateInstallPlan, func(candidateInstallPlan) error) (*candidateInstallTargetGuard, error) {
	return nil, fmt.Errorf("native deployment guard unavailable")
}
