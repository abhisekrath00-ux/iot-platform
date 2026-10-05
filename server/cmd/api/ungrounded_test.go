package main

import (
	"strings"
	"testing"
)

func TestUngroundedNote(t *testing.T) {
	if !strings.Contains(ungroundedNote("There are 3 open alerts.", 0), "no platform data") {
		t.Fatal("numbers without a tool call must be flagged")
	}
	for _, c := range []struct {
		r string
		n int
	}{{"There are 3 open alerts.", 1}, {"Hello, how can I help?", 0}, {"", 0}} {
		if ungroundedNote(c.r, c.n) != "" {
			t.Fatalf("%+v flagged", c)
		}
	}
}
