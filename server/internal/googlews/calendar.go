package googlews

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"
)

// Calendar read (calendar.readonly scope). Only what the on-call sync needs:
// event summaries and their start/end. The endpoint is injectable so tests
// run against a local simulator - this package never dials Google on its own
// in tests.

// CalendarEvent is one expanded calendar entry. All-day events (Google
// "date" fields) are read as UTC midnights; Google's all-day end date is
// exclusive, which matches the [Start, End) span the sync uses.
type CalendarEvent struct {
	Summary string
	Start   time.Time
	End     time.Time
}

// ListEvents returns the single (non-recurring-expanded) events of
// calendarID overlapping [from, to), cancelled entries excluded, in start
// order. Access is read-only; the caller supplies a fresh access token.
func (p *Provider) ListEvents(ctx context.Context, accessToken, calendarID string, from, to time.Time) ([]CalendarEvent, error) {
	base := p.CalendarURL
	if base == "" {
		base = "https://www.googleapis.com/calendar/v3"
	}
	u := fmt.Sprintf("%s/calendars/%s/events?timeMin=%s&timeMax=%s&singleEvents=true&orderBy=startTime&maxResults=50&fields=items(summary,start,end,status)",
		base, url.PathEscape(calendarID), url.QueryEscape(from.UTC().Format(time.RFC3339)), url.QueryEscape(to.UTC().Format(time.RFC3339)))
	req, err := http.NewRequestWithContext(ctx, "GET", u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	res, err := p.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if res.StatusCode == 403 {
		return nil, fmt.Errorf("google calendar refused access (403) - the connected grant may lack the calendar surface; reconnect including calendar on-call")
	}
	if res.StatusCode == 404 {
		return nil, fmt.Errorf("google calendar not found (404) - check the calendar id")
	}
	if res.StatusCode != 200 {
		return nil, fmt.Errorf("google calendar events: status %d", res.StatusCode)
	}
	var payload struct {
		Items []struct {
			Status  string `json:"status"`
			Summary string `json:"summary"`
			Start   struct {
				DateTime string `json:"dateTime"`
				Date     string `json:"date"`
			} `json:"start"`
			End struct {
				DateTime string `json:"dateTime"`
				Date     string `json:"date"`
			} `json:"end"`
		} `json:"items"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, fmt.Errorf("google calendar events: bad json: %w", err)
	}
	out := []CalendarEvent{}
	for _, it := range payload.Items {
		if it.Status == "cancelled" {
			continue
		}
		st, err1 := parseCalTime(it.Start.DateTime, it.Start.Date)
		en, err2 := parseCalTime(it.End.DateTime, it.End.Date)
		if err1 != nil || err2 != nil {
			continue // entries without a readable span cannot drive on-call
		}
		out = append(out, CalendarEvent{Summary: it.Summary, Start: st, End: en})
	}
	return out, nil
}

func parseCalTime(dt, d string) (time.Time, error) {
	if dt != "" {
		return time.Parse(time.RFC3339, dt)
	}
	if d != "" {
		return time.Parse("2006-01-02", d)
	}
	return time.Time{}, fmt.Errorf("no time")
}
