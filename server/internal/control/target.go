// Package control holds the control target model shared by the API and the flow engine.
// A target bounds what may be REQUESTED. Nothing here actuates anything.
package control

import (
	"errors"
	"fmt"
	"math"
)

// Target is one allowlisted thing a flow may ask to change.
type Target struct {
	ID            string    `json:"id"`
	Name          string    `json:"name"`
	Kind          string    `json:"kind"`
	GatewayID     string    `json:"gateway_id"`
	DeviceID      string    `json:"device_id"`
	PointID       string    `json:"point_id"`
	Min           *float64  `json:"min,omitempty"`
	Max           *float64  `json:"max,omitempty"`
	AllowedValues []float64 `json:"allowed_values,omitempty"`
	MaxPerHour    int       `json:"max_per_hour"`
	ApprovalMode  string    `json:"approval_mode"`
	Enabled       bool      `json:"enabled"`
}

// CheckValue refuses a value outside the target's bounds. Values are never clamped
// silently or passed through: out of range is a refusal.
func (t *Target) CheckValue(v float64) error {
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return errors.New("value is not a finite number")
	}
	if len(t.AllowedValues) > 0 {
		for _, a := range t.AllowedValues {
			if a == v {
				return nil
			}
		}
		return fmt.Errorf("value %v is not one of the allowed values", v)
	}
	if t.Min == nil || t.Max == nil {
		return errors.New("target has no bounds")
	}
	if v < *t.Min || v > *t.Max {
		return fmt.Errorf("value %v is outside %v..%v", v, *t.Min, *t.Max)
	}
	return nil
}
