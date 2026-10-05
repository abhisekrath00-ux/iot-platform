package main

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/abhisekrath00-ux/iot-platform/server/internal/llm"
)

// Opt-in eval against a real model: EVAL_LLM_URL=http://127.0.0.1:8081/v1 go test -run TestEvalAssistant ./cmd/api
// These cases come from real failures of the small local model.
func TestEvalAssistant(t *testing.T) {
	base := os.Getenv("EVAL_LLM_URL")
	if base == "" {
		t.Skip("set EVAL_LLM_URL to run against a real model")
	}
	cfg := llm.Config{BaseURL: base, Model: "qwen3-1.7b", Timeout: 280 * time.Second, NoThinking: true, Small: true}
	cases := []struct {
		name, text string
		check      func(m llm.Message) string
	}{
		{"greeting", "hello", func(m llm.Message) string {
			if len(m.ToolCalls) > 0 || strings.Contains(strings.ToLower(m.Content), "don't know") || m.Content == "" {
				return "should greet, got " + m.Content
			}
			return ""
		}},
		{"ai activity", "send me all AI activity", func(m llm.Message) string {
			if len(m.ToolCalls) == 0 && !strings.Contains(m.Content, "Settings") {
				return "should point to Settings > AI activity, got " + m.Content
			}
			return ""
		}},
		{"create device", "create a new device", func(m llm.Message) string {
			for _, c := range m.ToolCalls {
				if c.Func.Name == "create_asset" || c.Func.Name == "create_site" {
					return "must not create an asset/site for a device"
				}
			}
			if len(m.ToolCalls) == 0 && !strings.Contains(m.Content, "Add device") {
				return "should point to Add device, got " + m.Content
			}
			return ""
		}},
		{"capabilities", "what can you do", func(m llm.Message) string {
			if len(m.ToolCalls) == 0 && m.Content == "" {
				return "empty"
			}
			return ""
		}},
	}
	for _, c := range cases {
		if r, ok := cannedReply(c.text); ok { // answered without the model, as in runAgent
			t.Logf("%s -> canned %q", c.name, r)
			if why := c.check(llm.Message{Content: r}); why != "" {
				t.Errorf("%s: %s", c.name, why)
			}
			continue
		}
		msgs := []llm.Message{{Role: "system", Content: typedPrompt("admin")}, {Role: "user", Content: c.text}}
		m, err := llm.Chat(context.Background(), cfg, msgs, toolsForTurn(llm.Config{BaseURL: "http://ai-runtime:8090/v1", NoThinking: true, Small: true}, msgs))
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		t.Logf("%s -> content=%q calls=%v", c.name, m.Content, m.ToolCalls)
		if why := c.check(m); why != "" {
			t.Errorf("%s: %s", c.name, why)
		}
	}
}
