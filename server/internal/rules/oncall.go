package rules

import (
	"fmt"
	"time"
)

// Schedule hands on-call duty to each channel in turn. The first channel's shift starts at Anchor and
// each shift lasts ShiftHours. Anchor may be in the past or the future; the rotation extends both ways.
type Schedule struct {
	ID         string    `json:"id"`
	Name       string    `json:"name"`
	Anchor     time.Time `json:"anchor"`
	ShiftHours int       `json:"shift_hours"`
	ChannelIDs []string  `json:"channel_ids"`

	// Calendar sync override (optional; see EffectiveOnCall).
	CalChannelID string    `json:"cal_channel_id,omitempty"`
	CalUntil     time.Time `json:"cal_until,omitempty"`
	CalSyncedAt  time.Time `json:"cal_synced_at,omitempty"`
}

// OnCall returns the channel on duty at t and when that shift ends.
func OnCall(s Schedule, t time.Time) (channel string, shiftEnds time.Time, ok bool) {
	if s.ShiftHours < 1 || len(s.ChannelIDs) == 0 {
		return "", time.Time{}, false
	}
	shift := time.Duration(s.ShiftHours) * time.Hour
	n := int64(len(s.ChannelIDs))
	k := int64(t.Sub(s.Anchor) / shift)
	if t.Before(s.Anchor) && t.Sub(s.Anchor)%shift != 0 {
		k-- // floor, not truncate toward zero
	}
	idx := ((k % n) + n) % n
	return s.ChannelIDs[idx], s.Anchor.Add(time.Duration(k+1) * shift), true
}

// ValidateSchedule checks a schedule before it is saved.
func ValidateSchedule(s Schedule) error {
	if s.Name == "" || len(s.Name) > 80 {
		return fmt.Errorf("name is required (80 characters at most)")
	}
	if s.Anchor.IsZero() {
		return fmt.Errorf("anchor (when the first shift starts) is required")
	}
	if s.ShiftHours < 1 || s.ShiftHours > 720 {
		return fmt.Errorf("shift_hours must be 1-720")
	}
	if len(s.ChannelIDs) < 1 || len(s.ChannelIDs) > 20 {
		return fmt.Errorf("list 1-20 channels")
	}
	seen := map[string]bool{}
	for _, c := range s.ChannelIDs {
		if c == "" {
			return fmt.Errorf("channel ids must not be empty")
		}
		if seen[c] {
			// the same channel twice is allowed on purpose only if it is not adjacent; keep it simple
			return fmt.Errorf("channel %s is listed twice", c)
		}
		seen[c] = true
	}
	return nil
}

// Calendar-sync override columns (migration 0083). Empty CalChannelID means
// no override and the plain rotation applies.
// CalSyncedAt is when the calendar was last read successfully; an override
// older than the caller's staleness bound is ignored so a dead sync never
// pins the wrong person on duty.

// EffectiveOnCall resolves who is on duty: a fresh calendar override wins
// over the static rotation; anything missing, expired or stale falls back
// to the rotation. shiftEnds comes from the winning source.
func (s Schedule) EffectiveOnCall(t time.Time, maxStale time.Duration) (channel string, shiftEnds time.Time, ok bool) {
	if s.CalChannelID != "" && !s.CalUntil.IsZero() && t.Before(s.CalUntil) &&
		!s.CalSyncedAt.IsZero() && t.Sub(s.CalSyncedAt) <= maxStale {
		return s.CalChannelID, s.CalUntil, true
	}
	return OnCall(s, t)
}
