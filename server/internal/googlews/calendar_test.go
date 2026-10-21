package googlews

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestListEventsParsesSpans(t *testing.T) {
	sim := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer tok" {
			t.Errorf("missing bearer: %q", r.Header.Get("Authorization"))
		}
		if !strings.Contains(r.URL.Path, "/calendars/cal-1/events") {
			t.Errorf("path: %s", r.URL.Path)
		}
		if r.URL.Query().Get("singleEvents") != "true" {
			t.Errorf("singleEvents missing: %s", r.URL.RawQuery)
		}
		json.NewEncoder(w).Encode(map[string]any{"items": []map[string]any{
			{"summary": "On call: ops@hex.example", "start": map[string]string{"dateTime": "2026-10-21T08:00:00Z"}, "end": map[string]string{"dateTime": "2026-10-21T20:00:00Z"}},
			{"summary": "cancelled entry", "status": "cancelled", "start": map[string]string{"dateTime": "2026-10-21T09:00:00Z"}, "end": map[string]string{"dateTime": "2026-10-21T10:00:00Z"}},
			{"summary": "all day", "start": map[string]string{"date": "2026-10-22"}, "end": map[string]string{"date": "2026-10-23"}},
		}})
	}))
	t.Cleanup(sim.Close)
	p, _ := NewProvider("cid", "csecret", "https://hex.example/cb")
	p.CalendarURL = sim.URL
	from := time.Date(2026, 10, 21, 0, 0, 0, 0, time.UTC)
	evs, err := p.ListEvents(context.Background(), "tok", "cal-1", from, from.Add(48*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if len(evs) != 2 {
		t.Fatalf("cancelled must be skipped: %+v", evs)
	}
	if evs[0].Summary != "On call: ops@hex.example" || evs[0].Start.Hour() != 8 {
		t.Fatalf("first event: %+v", evs[0])
	}
	if evs[1].Summary != "all day" || evs[1].Start.Day() != 22 || evs[1].End.Day() != 23 {
		t.Fatalf("all-day event as UTC midnights: %+v", evs[1])
	}
}

func TestListEventsErrors(t *testing.T) {
	for code, want := range map[int]string{403: "calendar surface", 404: "not found", 500: "status 500"} {
		sim := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(code) }))
		p, _ := NewProvider("cid", "csecret", "https://hex.example/cb")
		p.CalendarURL = sim.URL
		_, err := p.ListEvents(context.Background(), "tok", "cal-1", time.Now(), time.Now().Add(time.Hour))
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Fatalf("%d: want %q in %v", code, want, err)
		}
		sim.Close()
	}
}
