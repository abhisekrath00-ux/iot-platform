// Package fleet holds the rollout state machine: stage validation,
// deterministic cohort selection, and campaign transitions. Delivery to
// gateways (broker retained messages) and ACK handling live at the edges of
// this package; the state machine itself is pure and fully tested.
package fleet

import (
	"errors"
	"fmt"
	"hash/fnv"
	"sort"
)

// ValidateStages requires ascending percents in (0,100], last exactly 100.
func ValidateStages(stages []int) error {
	if len(stages) == 0 || len(stages) > 10 {
		return errors.New("stages: need 1-10 stage percents")
	}
	prev := 0
	for i, p := range stages {
		if p <= prev || p > 100 {
			return fmt.Errorf("stages: must be strictly ascending percents (stage %d = %d)", i, p)
		}
		prev = p
	}
	if stages[len(stages)-1] != 100 {
		return errors.New("stages: last stage must be 100")
	}
	return nil
}

// Cohort deterministically decides whether a serial belongs in the given
// stage percent for a campaign. Stable across processes: a gateway never
// jumps buckets mid-campaign.
func Cohort(campaignID, serial string, stagePercent int) bool {
	h := fnv.New32a()
	h.Write([]byte(campaignID + "/" + serial))
	return int(h.Sum32()%100) < stagePercent
}

// CohortForStage selects the serials assigned exactly at this stage index:
// inside this stage's percent but outside every earlier stage's.
func CohortForStage(campaignID string, serials []string, stages []int, stageIndex int) []string {
	var out []string
	for _, s := range serials {
		if !Cohort(campaignID, s, stages[stageIndex]) {
			continue
		}
		earlier := false
		for j := 0; j < stageIndex; j++ {
			if Cohort(campaignID, s, stages[j]) {
				earlier = true
				break
			}
		}
		if !earlier {
			out = append(out, s)
		}
	}
	sort.Strings(out)
	return out
}

// Campaign states.
const (
	Draft   = "draft"
	Running = "running"
	Paused  = "paused"
	Done    = "done"
	Aborted = "aborted"
)

// Transition validates a campaign state change.
func Transition(from, to string) error {
	ok := map[string][]string{
		Draft:   {Running, Aborted},
		Running: {Paused, Done, Aborted},
		Paused:  {Running, Aborted},
	}[from]
	for _, t := range ok {
		if t == to {
			return nil
		}
	}
	return fmt.Errorf("campaign cannot go %s -> %s", from, to)
}

// Halted reports whether a running campaign must stop advancing because
// failures reached the threshold.
func Halted(failures, threshold int) bool {
	return threshold > 0 && failures >= threshold
}
