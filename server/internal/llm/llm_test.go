package llm

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestValidateBaseURL(t *testing.T) {
	for _, ok := range []string{"http://localhost:11434/v1", "https://api.openai.com/v1", "http://10.0.0.5:8000/v1", "http://ollama:11434/v1"} {
		if err := ValidateBaseURL(ok); err != nil {
			t.Errorf("%s: %v", ok, err)
		}
	}
	for _, bad := range []string{"", "ftp://x/v1", "http://169.254.169.254/latest", "http://[fe80::1]/v1", "http://u:p@host/v1", "http://", "http://0.0.0.0/v1", "javascript:alert(1)"} {
		if ValidateBaseURL(bad) == nil {
			t.Errorf("accepted %q", bad)
		}
	}
}

func TestChatToolCallsAndErrors(t *testing.T) {
	var got map[string]any
	var auth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth = r.Header.Get("Authorization")
		if r.URL.Path != "/v1/chat/completions" {
			http.NotFound(w, r)
			return
		}
		json.NewDecoder(r.Body).Decode(&got)
		if got["model"] == "boom" {
			http.Error(w, "bad key sk-abc", 401)
			return
		}
		w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"","tool_calls":[{"id":"1","type":"function","function":{"name":"f","arguments":"{\"a\":1}"}}]}}]}`))
	}))
	defer srv.Close()
	cfg := Config{BaseURL: srv.URL + "/v1/", Model: "m", APIKey: "sk-abc", Timeout: 5 * time.Second}
	m, err := Chat(context.Background(), cfg, []Message{{Role: "user", Content: "hi"}}, []Tool{{Name: "f", Description: "d", Parameters: map[string]any{"type": "object"}}})
	if err != nil || len(m.ToolCalls) != 1 || m.ToolCalls[0].Func.Name != "f" || m.ToolCalls[0].Func.Arguments != `{"a":1}` {
		t.Fatalf("%+v %v", m, err)
	}
	if auth != "Bearer sk-abc" || got["tool_choice"] != "auto" {
		t.Fatalf("auth=%q body=%v", auth, got)
	}
	cfg.Model = "boom"
	if _, err = Chat(context.Background(), cfg, nil, nil); err == nil || strings.Contains(err.Error(), "sk-abc") || !strings.Contains(err.Error(), "401") {
		t.Fatalf("error must say 401 and never echo the key: %v", err)
	}
	// not a chat completion
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(`{"hello":1}`)) }))
	defer bad.Close()
	if _, err := Chat(context.Background(), Config{BaseURL: bad.URL, Model: "m"}, nil, nil); err == nil {
		t.Fatal("a non-OpenAI reply must be an error")
	}
	// redirects are not followed
	redir := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, "http://169.254.169.254/", 302) }))
	defer redir.Close()
	if _, err := Chat(context.Background(), Config{BaseURL: redir.URL, Model: "m"}, nil, nil); err == nil {
		t.Fatal("a redirect must not be followed")
	}
}
