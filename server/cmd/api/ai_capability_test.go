package main

import (
	"testing"

	"github.com/abhisekrath00-ux/iot-platform/server/internal/aitools"
	"github.com/abhisekrath00-ux/iot-platform/server/internal/llm"
)

// Small-model workarounds must apply only to small models; a hosted or "full" model gets the full path.
func TestCapabilityGatesSmallModelMode(t *testing.T) {
	for _, c := range []struct {
		cfg   aiConfig
		small bool
	}{
		{aiConfig{BaseURL: "http://ai-runtime:8090/v1"}, true},                         // auto, bundled local
		{aiConfig{BaseURL: "http://ai-runtime:8090/v1", Capability: "auto"}, true},     // auto
		{aiConfig{BaseURL: "https://openrouter.ai/api/v1"}, false},                     // auto, hosted
		{aiConfig{BaseURL: "https://openrouter.ai/api/v1", Capability: "small"}, true}, // forced small
		{aiConfig{BaseURL: "http://10.0.0.5:11434/v1", Capability: "full"}, false},     // big model on the LAN
		{aiConfig{BaseURL: "http://10.0.0.5:11434/v1"}, true},                          // auto, private network
	} {
		if got := isSmallModel(c.cfg); got != c.small {
			t.Errorf("%+v: small=%v want %v", c.cfg, got, c.small)
		}
	}
	for in, ok := range map[string]bool{"auto": true, "small": true, "full": true, "": false, "huge": false} {
		if validCapability(in) != ok {
			t.Errorf("validCapability(%q) = %v", in, !ok)
		}
	}
}

func TestFullModelGetsFullPathAndNoShortcuts(t *testing.T) {
	full := llm.Config{BaseURL: "https://openrouter.ai/api/v1", Model: "big", Small: false}
	small := llm.Config{BaseURL: "http://ai-runtime:8090/v1", Model: "q", Small: true, NoThinking: true}
	if typedMode(full) || !typedMode(small) {
		t.Fatalf("typedMode: full=%v small=%v", typedMode(full), typedMode(small))
	}
	msgs := []llm.Message{{Role: "user", Content: "create a new device"}}
	if got := len(toolsForTurn(full, msgs)); got != len(assistantTools) {
		t.Errorf("full model must see the whole menu: %d of %d", got, len(assistantTools))
	}
	if got := len(toolsForTurn(small, msgs)); got > 12 || got >= len(aitools.Specs()) {
		t.Errorf("small model menu should be short: %d", got)
	}
	if len(toolsFor(full)) != len(assistantTools) {
		t.Errorf("full toolsFor must be the generic set")
	}
	if len(systemPrompt("admin")) < 3*len(typedPrompt("admin")) {
		t.Errorf("the full prompt should be much larger than the compact one")
	}
}
