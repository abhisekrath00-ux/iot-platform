// Package health turns a few raw facts about a device into a 0-100 score with
// the reasons behind it. It is pure so the rules are easy to test and to show
// to users; the API gathers the inputs.
package health

import "time"

type Input struct {
	LastSeen        *time.Time // newest telemetry sample, nil if none
	Now             time.Time
	Interval        time.Duration // expected poll interval (0 = 60s)
	Samples         int           // recent samples inspected
	MeasuredSamples int           // of those, quality == measured
	GatewayOnline   bool
}

type Factor struct {
	Name   string `json:"name"`
	Score  int    `json:"score"` // 0-100
	Weight int    `json:"weight"`
	Detail string `json:"detail"`
}

type Result struct {
	Score   int      `json:"score"`
	Status  string   `json:"status"` // healthy|degraded|critical|offline
	Factors []Factor `json:"factors"`
}

func Score(in Input) Result {
	iv := in.Interval
	if iv <= 0 {
		iv = 60 * time.Second
	}
	var fs []Factor

	// Freshness: full marks within 3 polls, linear to zero at 30 polls.
	fresh := Factor{Name: "freshness", Weight: 50}
	if in.LastSeen == nil {
		fresh.Score, fresh.Detail = 0, "no data received yet"
	} else {
		age := in.Now.Sub(*in.LastSeen)
		polls := float64(age) / float64(iv)
		switch {
		case polls <= 3:
			fresh.Score = 100
		case polls >= 30:
			fresh.Score = 0
		default:
			fresh.Score = int(100 * (30 - polls) / 27)
		}
		fresh.Detail = "last sample " + age.Round(time.Second).String() + " ago"
	}
	fs = append(fs, fresh)

	q := Factor{Name: "data quality", Weight: 30}
	if in.Samples == 0 {
		q.Score, q.Detail = 0, "no samples to judge"
	} else {
		q.Score = 100 * in.MeasuredSamples / in.Samples
		q.Detail = "measured samples in recent window"
	}
	fs = append(fs, q)

	g := Factor{Name: "gateway link", Weight: 20, Detail: "gateway offline"}
	if in.GatewayOnline {
		g.Score, g.Detail = 100, "gateway online"
	}
	fs = append(fs, g)

	total, w := 0, 0
	for _, f := range fs {
		total += f.Score * f.Weight
		w += f.Weight
	}
	r := Result{Score: total / w, Factors: fs}
	switch {
	case in.LastSeen == nil || fresh.Score == 0:
		r.Status = "offline"
	case r.Score >= 80 && fresh.Score >= 80: // stale data is never "healthy"
		r.Status = "healthy"
	case r.Score >= 50 || fresh.Score < 80 && r.Score >= 30:
		r.Status = "degraded"
	default:
		r.Status = "critical"
	}
	return r
}
