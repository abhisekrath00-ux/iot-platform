package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/abhisekrath00-ux/iot-platform/server/internal/llm"
)

func TestValidateAgentCallBoundary(t *testing.T) {
	mk := func(name, args string) llm.ToolCall {
		tc := llm.ToolCall{ID: "one", Type: "function"}
		tc.Func.Name = name
		tc.Func.Arguments = args
		return tc
	}
	offered := map[string]bool{"list_devices": true}
	cases := []struct {
		name    string
		tc      llm.ToolCall
		allowed bool
	}{
		{"valid", mk("list_devices", `{}`), true},
		{"unoffered native call", mk("api_request", `{"method":"GET","path":"/v1/devices"}`), false},
		{"invented name", mk("approve_command", `{}`), false},
		{"array", mk("list_devices", `[]`), false},
		{"null", mk("list_devices", `null`), false},
		{"broken", mk("list_devices", `{`), false},
		{"too large", mk("list_devices", `{"q":"`+strings.Repeat("x", maxToolArguments)+`"}`), false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := validateAgentCall(context.Background(), c.tc, offered)
			if (err == nil) != c.allowed {
				t.Fatalf("allowed=%v err=%v", c.allowed, err)
			}
		})
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if validateAgentCall(ctx, mk("list_devices", `{}`), offered) == nil {
		t.Fatal("cancelled call passed")
	}
	tc := mk("list_devices", `{}`)
	tc.Type = "custom"
	if validateAgentCall(context.Background(), tc, offered) == nil {
		t.Fatal("custom call type passed")
	}
	tc = mk("list_devices", `{}`)
	tc.ID = "bad\nid"
	if validateAgentCall(context.Background(), tc, offered) == nil {
		t.Fatal("bad ID passed")
	}
}

func TestCanonicalAgentLoopSignature(t *testing.T) {
	a := llm.ToolCall{}
	a.Func.Name = "list_devices"
	a.Func.Arguments = `{"q":"meter","tag":"north"}`
	b := a
	b.Func.Arguments = "{ \"tag\": \"north\", \"q\": \"meter\" }"
	if canonicalCallSignature(a) != canonicalCallSignature(b) {
		t.Fatal("whitespace/key order bypassed loop detection")
	}
	b.Func.Arguments = `{"q":"other","tag":"north"}`
	if canonicalCallSignature(a) == canonicalCallSignature(b) {
		t.Fatal("different calls conflated")
	}
}

// Scripted-provider runs prove these checks are wired into execution, not just helpers.
func TestAgentRejectsUnofferedAndMalformedNativeCalls(t *testing.T) {
	t.Setenv("AI_TOOL_MODE", "generic")
	for _, c := range []struct{ name, args string }{
		{"list_devices", `{}`}, // exists in typed registry, but not advertised in generic mode
		{"set_plan", `[]`},
		{"set_plan", `{"steps":["` + strings.Repeat("x", maxToolArguments) + `"]}`},
	} {
		t.Run(c.name+string(rune(len(c.args))), func(t *testing.T) {
			fm := &fakeModel{script: []map[string]any{toolMsg("1", c.name, c.args), {"role": "assistant", "content": "stopped"}}}
			httpServer := httptest.NewServer(fm)
			defer httpServer.Close()
			s := &server{}
			got := s.runAgent(context.Background(), llm.Config{BaseURL: httpServer.URL, Model: "fake"}, "t", "u", "admin", []llm.Message{{Role: "user", Content: "run a tool"}}, runCtx{})
			if got.err != nil || len(got.trace) != 1 || got.trace[0].Status != "refused" || len(got.plan) > 0 {
				t.Fatalf("%+v", got)
			}
		})
	}
}

func TestAgentRejectsOversizedBatchBeforeAnyExecution(t *testing.T) {
	t.Setenv("AI_TOOL_MODE", "generic")
	calls := []any{}
	for i := 0; i < maxAgentTools+1; i++ {
		m := toolMsg("id", "set_plan", `{"steps":["must not run"]}`)
		calls = append(calls, m["tool_calls"].([]any)[0])
	}
	fm := &fakeModel{script: []map[string]any{{"role": "assistant", "tool_calls": calls}}}
	hs := httptest.NewServer(fm)
	defer hs.Close()
	got := (&server{}).runAgent(context.Background(), llm.Config{BaseURL: hs.URL, Model: "fake"}, "t", "u", "admin", []llm.Message{{Role: "user", Content: "plan"}}, runCtx{})
	if got.err == nil || len(got.plan) > 0 || got.tools != 0 {
		t.Fatalf("batch executed: %+v", got)
	}
}

func TestAgentCancellationDoesNotReachProvider(t *testing.T) {
	t.Setenv("AI_TOOL_MODE", "generic")
	requests := 0
	hs := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { requests++; w.Write([]byte(`{}`)) }))
	defer hs.Close()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	got := (&server{}).runAgent(ctx, llm.Config{BaseURL: hs.URL, Model: "fake"}, "t", "u", "admin", []llm.Message{{Role: "user", Content: "work"}}, runCtx{})
	if got.err == nil || requests != 0 {
		t.Fatalf("requests=%d result=%+v", requests, got)
	}
}

func TestLocalCatalogModelCapabilities(t *testing.T) {
	for _, m := range []string{"qwen3-4b", "qwen3-8b", "gpt-oss-20b", "gpt-oss-120b"} {
		c := aiConfig{BaseURL: "http://ai-runtime:8090/v1", Model: m}
		if isSmallModel(c) {
			t.Fatalf("catalog model %s got small-only workarounds", m)
		}
		c.Capability = "small"
		if !isSmallModel(c) {
			t.Fatal("explicit small override ignored")
		}
	}
	if !isSmallModel(aiConfig{BaseURL: "http://ai-runtime:8090/v1", Model: "qwen3-1.7b"}) {
		t.Fatal("1.7B must retain compact mode")
	}
	if isSmallModel(aiConfig{BaseURL: "http://ai-runtime:8090/v1", Model: "qwen3-1.7b", Capability: "full"}) {
		t.Fatal("full override ignored")
	}
}
