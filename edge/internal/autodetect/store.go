package autodetect

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/abhisekrath00-ux/iot-platform/edge/internal/driver"
)

// Finding is one thing the edge saw. It is a proposal, never a configured device.
type Finding struct {
	Key       string         `json:"key"`
	Kind      string         `json:"kind"` // modbus-rtu | lan | bacnet
	Port      string         `json:"port,omitempty"`
	Baud      int            `json:"baud,omitempty"`
	Parity    string         `json:"parity,omitempty"`
	Address   int            `json:"address,omitempty"`
	Host      string         `json:"host,omitempty"`
	NetPort   int            `json:"net_port,omitempty"`
	Service   string         `json:"service,omitempty"`
	Instance  uint32         `json:"instance,omitempty"`
	Vendor    int            `json:"vendor,omitempty"`
	Matches   []driver.Match `json:"matches,omitempty"`
	Confident bool           `json:"confident,omitempty"`
	Profile   string         `json:"profile,omitempty"` // best profile id when confident
	State     string         `json:"state"`             // suggested | auto-added | ignored
	FirstSeen time.Time      `json:"first_seen"`
	LastSeen  time.Time      `json:"last_seen"`
}

// State is the persisted list plus bookkeeping about the last run.
type State struct {
	Findings  []Finding         `json:"findings"`
	LastRun   time.Time         `json:"last_run"`
	LastNote  []string          `json:"last_notes,omitempty"`
	Published map[string]string `json:"published,omitempty"` // kind+scope -> hash of the last published set
}

// Store keeps State in one JSON file, written atomically.
type Store struct {
	mu   sync.Mutex
	path string
	st   State
}

func OpenStore(path string) *Store {
	s := &Store{path: path}
	if b, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(b, &s.st)
	}
	if s.st.Published == nil {
		s.st.Published = map[string]string{}
	}
	return s
}

func (s *Store) Snapshot() State {
	s.mu.Lock()
	defer s.mu.Unlock()
	c := s.st
	c.Findings = append([]Finding(nil), s.st.Findings...)
	sort.Slice(c.Findings, func(i, j int) bool { return c.Findings[i].Key < c.Findings[j].Key })
	return c
}

// Merge records the findings of one run. A finding that already exists keeps its
// first_seen and state (an ignored one stays ignored). Returns the keys that are new.
func (s *Store) Merge(now time.Time, found []Finding, notes []string) []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	idx := map[string]int{}
	for i, f := range s.st.Findings {
		idx[f.Key] = i
	}
	var fresh []string
	for _, f := range found {
		if i, ok := idx[f.Key]; ok {
			old := s.st.Findings[i]
			f.FirstSeen, f.State, f.LastSeen = old.FirstSeen, old.State, now
			s.st.Findings[i] = f
			continue
		}
		f.FirstSeen, f.State, f.LastSeen = now, "suggested", now
		s.st.Findings = append(s.st.Findings, f)
		fresh = append(fresh, f.Key)
	}
	s.st.LastRun, s.st.LastNote = now, notes
	_ = s.saveLocked()
	return fresh
}

func (s *Store) MarkState(key, state string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.st.Findings {
		if s.st.Findings[i].Key == key {
			s.st.Findings[i].State = state
		}
	}
	_ = s.saveLocked()
}

// PublishedHash returns the hash last published for a scope; SetPublished records one.
func (s *Store) PublishedHash(scope string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.st.Published[scope]
}

func (s *Store) SetPublished(scope, h string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.st.Published[scope] = h
	_ = s.saveLocked()
}

func (s *Store) saveLocked() error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return err
	}
	b, _ := json.MarshalIndent(s.st, "", "  ")
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}
