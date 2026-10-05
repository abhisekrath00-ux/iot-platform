// Package llm is a small client for OpenAI-compatible chat endpoints with tool calling: OpenAI and
// the many services that copy its API, and local servers such as Ollama, llama.cpp and vLLM.
// It sends nothing the caller did not pass in and keeps no state.
package llm

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"syscall"
	"time"
)

type ToolCall struct {
	ID   string `json:"id"`
	Type string `json:"type"`
	Func struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

type Message struct {
	Role       string     `json:"role"` // system | user | assistant | tool
	Content    string     `json:"content"`
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
}

type Tool struct {
	Name        string
	Description string
	Parameters  map[string]any // JSON schema
}

type Config struct {
	BaseURL string
	Model   string
	APIKey  string // may be empty (local servers)
	Timeout time.Duration
	// NoThinking asks a local Qwen3-style runtime to skip its reasoning preamble. Sent only when set,
	// because hosted OpenAI-style endpoints reject fields they do not know.
	NoThinking bool
}

const maxResponse = 1 << 20

// ValidateBaseURL checks an admin-supplied endpoint. Private and loopback hosts are allowed on
// purpose (a local model server is the air-gapped case); link-local addresses, where cloud
// metadata services live, are refused here and again when connecting.
func ValidateBaseURL(raw string) error {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil {
		return errors.New("base URL must be http:// or https:// with a host and no credentials")
	}
	if ip := net.ParseIP(u.Hostname()); ip != nil && blocked(ip) {
		return errors.New("that address is not allowed")
	}
	return nil
}

func blocked(ip net.IP) bool {
	return ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsUnspecified() || ip.IsMulticast()
}

func client(timeout time.Duration) *http.Client {
	if timeout <= 0 {
		timeout = 120 * time.Second
	}
	d := &net.Dialer{Timeout: 10 * time.Second, Control: func(_, address string, _ syscall.RawConn) error {
		host, _, err := net.SplitHostPort(address)
		if err != nil {
			return err
		}
		if ip := net.ParseIP(host); ip != nil && blocked(ip) {
			return errors.New("connection to a link-local address refused")
		}
		return nil
	}}
	return &http.Client{Timeout: timeout, Transport: &http.Transport{DialContext: d.DialContext, Proxy: nil},
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
}

// Chat sends one request and returns the model's next message (text and/or tool calls).
func Chat(ctx context.Context, cfg Config, msgs []Message, tools []Tool) (Message, error) {
	if err := ValidateBaseURL(cfg.BaseURL); err != nil {
		return Message{}, err
	}
	body := buildBody(cfg, msgs, tools, false)
	b, _ := json.Marshal(body)
	req, err := http.NewRequestWithContext(ctx, "POST", strings.TrimRight(strings.TrimSpace(cfg.BaseURL), "/")+"/chat/completions", bytes.NewReader(b))
	if err != nil {
		return Message{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	if cfg.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+cfg.APIKey)
	}
	resp, err := client(cfg.Timeout).Do(req)
	if err != nil {
		return Message{}, fmt.Errorf("model request failed: %v", scrub(err.Error(), cfg.APIKey))
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, maxResponse))
	if resp.StatusCode != 200 {
		return Message{}, fmt.Errorf("model endpoint returned %d: %s", resp.StatusCode, scrub(truncate(string(raw), 300), cfg.APIKey))
	}
	var out struct {
		Choices []struct {
			Message Message `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(raw, &out); err != nil || len(out.Choices) == 0 {
		return Message{}, errors.New("model endpoint did not return an OpenAI-style chat completion")
	}
	m := out.Choices[0].Message
	m.Role = "assistant"
	return m, nil
}

func buildBody(cfg Config, msgs []Message, tools []Tool, stream bool) map[string]any {
	body := map[string]any{"model": cfg.Model, "messages": msgs, "temperature": 0.2}
	if len(tools) > 0 {
		var ts []map[string]any
		for _, t := range tools {
			ts = append(ts, map[string]any{"type": "function", "function": map[string]any{"name": t.Name, "description": t.Description, "parameters": t.Parameters}})
		}
		body["tools"], body["tool_choice"] = ts, "auto"
	}
	if cfg.NoThinking {
		body["chat_template_kwargs"] = map[string]any{"enable_thinking": false}
	}
	if stream {
		body["stream"] = true
	}
	return body
}

// ChatStream is Chat with server-sent events: onDelta gets each piece of answer text as it arrives
// (tool-call fragments are assembled silently). The returned message is the same as Chat's.
func ChatStream(ctx context.Context, cfg Config, msgs []Message, tools []Tool, onDelta func(string)) (Message, error) {
	if err := ValidateBaseURL(cfg.BaseURL); err != nil {
		return Message{}, err
	}
	b, _ := json.Marshal(buildBody(cfg, msgs, tools, true))
	req, err := http.NewRequestWithContext(ctx, "POST", strings.TrimRight(strings.TrimSpace(cfg.BaseURL), "/")+"/chat/completions", bytes.NewReader(b))
	if err != nil {
		return Message{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	if cfg.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+cfg.APIKey)
	}
	resp, err := client(cfg.Timeout).Do(req)
	if err != nil {
		return Message{}, fmt.Errorf("model request failed: %v", scrub(err.Error(), cfg.APIKey))
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, maxResponse))
		return Message{}, fmt.Errorf("model endpoint returned %d: %s", resp.StatusCode, scrub(truncate(string(raw), 300), cfg.APIKey))
	}
	return readStream(io.LimitReader(resp.Body, 4*maxResponse), onDelta)
}

func readStream(r io.Reader, onDelta func(string)) (Message, error) {
	m := Message{Role: "assistant"}
	var text strings.Builder
	calls := map[int]*ToolCall{}
	var order []int
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	done := false
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "[DONE]" {
			done = true
			break
		}
		var ch struct {
			Choices []struct {
				Delta struct {
					Content   string `json:"content"`
					ToolCalls []struct {
						Index int    `json:"index"`
						ID    string `json:"id"`
						Func  struct {
							Name      string `json:"name"`
							Arguments string `json:"arguments"`
						} `json:"function"`
					} `json:"tool_calls"`
				} `json:"delta"`
			} `json:"choices"`
		}
		if json.Unmarshal([]byte(data), &ch) != nil || len(ch.Choices) == 0 {
			continue
		}
		d := ch.Choices[0].Delta
		if d.Content != "" {
			text.WriteString(d.Content)
			if onDelta != nil {
				onDelta(d.Content)
			}
		}
		for _, t := range d.ToolCalls {
			c := calls[t.Index]
			if c == nil {
				c = &ToolCall{Type: "function"}
				calls[t.Index] = c
				order = append(order, t.Index)
			}
			if t.ID != "" {
				c.ID = t.ID
			}
			c.Func.Name += t.Func.Name
			c.Func.Arguments += t.Func.Arguments
		}
	}
	if err := sc.Err(); err != nil {
		return Message{}, fmt.Errorf("model stream broke: %v", err)
	}
	if !done && text.Len() == 0 && len(calls) == 0 {
		return Message{}, errors.New("model endpoint did not return an OpenAI-style chat stream")
	}
	m.Content = text.String()
	for _, i := range order {
		if calls[i].ID == "" {
			calls[i].ID = fmt.Sprintf("call_%d", i)
		}
		m.ToolCalls = append(m.ToolCalls, *calls[i])
	}
	return m, nil
}

func scrub(s, key string) string {
	if key != "" {
		s = strings.ReplaceAll(s, key, "[key]")
	}
	return s
}

func truncate(s string, n int) string {
	if len(s) > n {
		return s[:n] + "..."
	}
	return s
}
