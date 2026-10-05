package llm

import (
	"context"
	"encoding/json"

	"os"
	"strings"
)

// Provider is what the agent loop needs from a model. The OpenAI-compatible client is the only
// implementation shipped (llama.cpp, Ollama, vLLM and hosted endpoints all speak it); a different
// runtime can be added by implementing this without touching the agent.
type Provider interface {
	Name() string
	Chat(ctx context.Context, msgs []Message, tools []Tool) (Message, error)
	ChatStream(ctx context.Context, msgs []Message, tools []Tool, onDelta func(string)) (Message, error)
}

// OpenAICompat is the OpenAI-compatible chat provider.
type OpenAICompat struct{ Cfg Config }

func (p OpenAICompat) Name() string { return p.Cfg.Model }
func (p OpenAICompat) Chat(ctx context.Context, msgs []Message, tools []Tool) (Message, error) {
	return Chat(ctx, p.Cfg, msgs, tools)
}
func (p OpenAICompat) ChatStream(ctx context.Context, msgs []Message, tools []Tool, onDelta func(string)) (Message, error) {
	return ChatStream(ctx, p.Cfg, msgs, tools, onDelta)
}

// WithFallback tries the primary provider and, only when it fails with an error (unreachable,
// timeout, non-200), the secondary once. It never retries an answer that arrived. Streamed text
// from a failed primary may already have been shown, so the stream path falls back only when the
// primary produced nothing.
type WithFallback struct{ Primary, Secondary Provider }

func (p WithFallback) Name() string {
	return p.Primary.Name() + " (fallback " + p.Secondary.Name() + ")"
}
func (p WithFallback) Chat(ctx context.Context, msgs []Message, tools []Tool) (Message, error) {
	m, err := p.Primary.Chat(ctx, msgs, tools)
	if err != nil && ctx.Err() == nil {
		return p.Secondary.Chat(ctx, msgs, tools)
	}
	return m, err
}
func (p WithFallback) ChatStream(ctx context.Context, msgs []Message, tools []Tool, onDelta func(string)) (Message, error) {
	got := false
	m, err := p.Primary.ChatStream(ctx, msgs, tools, func(d string) { got = true; onDelta(d) })
	if err != nil && !got && ctx.Err() == nil {
		return p.Secondary.ChatStream(ctx, msgs, tools, onDelta)
	}
	return m, err
}

// FallbackFromEnv returns the optional secondary model configured by AI_FALLBACK_BASE_URL and
// AI_FALLBACK_MODEL (AI_FALLBACK_KEY optional). Off unless both are set; the URL gets the same
// validation as the primary.
func FallbackFromEnv(timeout Config) (Config, bool) {
	u, m := os.Getenv("AI_FALLBACK_BASE_URL"), os.Getenv("AI_FALLBACK_MODEL")
	if u == "" || m == "" || ValidateBaseURL(u) != nil {
		return Config{}, false
	}
	return Config{BaseURL: u, Model: m, APIKey: os.Getenv("AI_FALLBACK_KEY"), Timeout: timeout.Timeout, NoThinking: timeout.NoThinking}, true
}

// ParseTextToolCall implements the structured-output fallback for models without native tool
// calling: a reply that is (or contains, possibly in a code fence or <tool_call> tags) a JSON object
// {"tool":"name","arguments":{...}} becomes a tool call. The name must be one the caller offered;
// the arguments still go through the normal schema validation and policy before anything runs.
func ParseTextToolCall(content string, offered map[string]bool) (ToolCall, bool) {
	s := strings.TrimSpace(content)
	if s == "" || len(s) > 8000 {
		return ToolCall{}, false
	}
	for _, o := range jsonObjects(s) {
		var v map[string]json.RawMessage
		if json.Unmarshal([]byte(o), &v) != nil {
			continue
		}
		var name string
		for _, k := range []string{"tool", "name", "function"} {
			if raw, ok := v[k]; ok {
				_ = json.Unmarshal(raw, &name)
				if name != "" {
					break
				}
			}
		}
		if !offered[name] {
			continue
		}
		args := json.RawMessage("{}")
		for _, k := range []string{"arguments", "args", "parameters"} {
			if raw, ok := v[k]; ok {
				if len(raw) > 0 && raw[0] == '"' { // arguments sent as a JSON string
					var inner string
					if json.Unmarshal(raw, &inner) == nil {
						raw = json.RawMessage(inner)
					}
				}
				args = raw
				break
			}
		}
		var tc ToolCall
		tc.ID, tc.Type = "text-call", "function"
		tc.Func.Name, tc.Func.Arguments = name, string(args)
		return tc, true
	}
	return ToolCall{}, false
}

// jsonObjects returns the top-level {...} spans in s, balanced and string-aware.
func jsonObjects(s string) []string {
	var out []string
	for i := 0; i < len(s); i++ {
		if s[i] != '{' {
			continue
		}
		depth, inStr, esc := 0, false, false
		for j := i; j < len(s); j++ {
			c := s[j]
			switch {
			case inStr:
				if esc {
					esc = false
				} else if c == '\\' {
					esc = true
				} else if c == '"' {
					inStr = false
				}
			case c == '"':
				inStr = true
			case c == '{':
				depth++
			case c == '}':
				depth--
				if depth == 0 {
					out = append(out, s[i:j+1])
					i = j
					j = len(s)
				}
			}
		}
	}
	return out
}

// ToolsForMCP converts tool definitions to the MCP tools/list shape.
func ToolsForMCP(ts []Tool) []map[string]any {
	out := make([]map[string]any, 0, len(ts))
	for _, t := range ts {
		out = append(out, map[string]any{"name": t.Name, "description": t.Description, "inputSchema": t.Parameters})
	}
	return out
}
