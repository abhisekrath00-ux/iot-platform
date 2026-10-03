package main

import (
	"strings"
	"testing"
	"time"
)

func TestValidateAttributes(t *testing.T) {
	if err := validateAttributes(map[string]any{"serial": "A1", "floor": 3.0, "critical": true}); err != nil {
		t.Fatal(err)
	}
	bad := []map[string]any{
		{"1bad": "x"},
		{"bad key": "x"},
		{"nested": map[string]any{"a": 1}},
		{"list": []any{1}},
		{"nul": nil},
		{"long": strings.Repeat("x", 257)},
	}
	for i, b := range bad {
		if validateAttributes(b) == nil {
			t.Errorf("case %d accepted: %v", i, b)
		}
	}
	many := map[string]any{}
	for i := 0; i < 33; i++ {
		many["k"+strings.Repeat("a", i)] = "v"
	}
	if validateAttributes(many) == nil {
		t.Error("33 attributes accepted")
	}
}

func TestShadowStale(t *testing.T) {
	if shadowStale(2*time.Minute, 60) {
		t.Error("2 min old at 60 s interval is fresh")
	}
	if !shadowStale(4*time.Minute, 60) {
		t.Error("4 min old at 60 s interval is stale")
	}
	if shadowStale(10*time.Minute, 0) || !shadowStale(16*time.Minute, 0) {
		t.Error("default limit is 15 minutes")
	}
	if shadowStale(20*time.Second, 1) {
		t.Error("very short intervals get a 30 s floor")
	}
}
