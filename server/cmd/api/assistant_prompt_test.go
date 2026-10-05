package main

import (
	"strings"
	"testing"
)

// Eval-style checks from the transcript where the assistant invented menus and said a site could not
// be created. They pin the rules and facts the prompt must carry; they do not run a model.
func TestPromptsCarryGroundingStyleAndFacts(t *testing.T) {
	for name, p := range map[string]string{"generic": systemPrompt("admin"), "typed": typedPrompt("admin")} {
		wants := []string{"Never invent menus", "I am not sure", "8 lines or fewer", "no colour"}
		if name == "generic" {
			wants = []string{"Never invent menus", "I am not sure", "not possible", "8 lines or fewer", "no \"Summary of what I did\"",
				"there is no site colour", "Create site", "A customer is an external organisation", "I cannot"}
		}
		for _, want := range wants {
			if !strings.Contains(strings.ToLower(p), strings.ToLower(want)) {
				t.Errorf("%s prompt lacks %q", name, want)
			}
		}
	}
	if len(typedPrompt("admin")) > 2200 {
		t.Errorf("typed prompt grew to %d bytes; it must fit a small model's context", len(typedPrompt("admin")))
	}
	if !strings.Contains(systemPrompt("admin"), "POST /v1/sites") {
		t.Error("generic prompt must list the site change as allowed")
	}
}
