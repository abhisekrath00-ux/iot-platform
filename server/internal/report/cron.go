package report

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// NextRun evaluates a 5-field cron expression (minute hour day month weekday;
// "*", "*/n", comma lists, and plain numbers) and returns the first matching
// minute strictly after `from`. Deliberately small: no ranges, no names.
func NextRun(expr string, from time.Time) (time.Time, error) {
	fields := strings.Fields(expr)
	if len(fields) != 5 {
		return time.Time{}, fmt.Errorf("cron: want 5 fields, got %d", len(fields))
	}
	ranges := [5][2]int{{0, 59}, {0, 23}, {1, 31}, {1, 12}, {0, 6}}
	match := make([][64]bool, 5)
	for i, f := range fields {
		for _, part := range strings.Split(f, ",") {
			step := 1
			if strings.Contains(part, "/") {
				sp := strings.SplitN(part, "/", 2)
				if sp[0] != "*" {
					return time.Time{}, fmt.Errorf("cron: %q must start with *", part)
				}
				n, err := strconv.Atoi(sp[1])
				if err != nil || n < 1 {
					return time.Time{}, fmt.Errorf("cron: bad step %q", part)
				}
				step = n
			} else if part != "*" {
				n, err := strconv.Atoi(part)
				if err != nil || n < ranges[i][0] || n > ranges[i][1] {
					return time.Time{}, fmt.Errorf("cron: %q out of range", part)
				}
				match[i][n] = true
				continue
			}
			for n := ranges[i][0]; n <= ranges[i][1]; n += step {
				match[i][n] = true
			}
		}
	}
	// Scan forward minute by minute, bounded to 366 days.
	t := from.Truncate(time.Minute).Add(time.Minute)
	for i := 0; i < 366*24*60; i++ {
		dow := int(t.Weekday())
		if match[0][t.Minute()] && match[1][t.Hour()] && match[2][t.Day()] &&
			match[3][int(t.Month())] && match[4][dow] {
			return t, nil
		}
		t = t.Add(time.Minute)
	}
	return time.Time{}, fmt.Errorf("cron: no match within a year")
}
