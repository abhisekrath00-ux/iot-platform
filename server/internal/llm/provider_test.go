package llm

import (
	"context"
	"errors"
	"testing"
)

func TestParseTextToolCall(t *testing.T) {
	off := map[string]bool{"list_alerts": true, "create_site": true}
	for _, c := range []struct {
		in, name string
		ok       bool
	}{
		{`{"type":"tool_call","tool":"list_alerts","arguments":{"status":"open"}}`, "list_alerts", true},
		{"Sure.\n```json\n{\"name\":\"create_site\",\"arguments\":{\"name\":\"A {x}\"}}\n```", "create_site", true},
		{`<tool_call>{"tool":"list_alerts","args":"{\"status\":\"open\"}"}</tool_call>`, "list_alerts", true},
		{`{"tool":"rm_rf","arguments":{}}`, "", false},
		{`There are 3 alerts.`, "", false},
		{`{"tool":"list_alerts"`, "", false},
	} {
		tc, ok := ParseTextToolCall(c.in, off)
		if ok != c.ok || tc.Func.Name != c.name {
			t.Errorf("%q: got %v %q", c.in, ok, tc.Func.Name)
		}
	}
	tc, _ := ParseTextToolCall(`<tool_call>{"tool":"list_alerts","args":"{\"status\":\"open\"}"}</tool_call>`, off)
	if tc.Func.Arguments != `{"status":"open"}` {
		t.Errorf("args %q", tc.Func.Arguments)
	}
}

type fake struct {
	err  error
	text string
	n    int
}

func (f *fake) Name() string { return "f" }
func (f *fake) Chat(context.Context, []Message, []Tool) (Message, error) {
	f.n++
	return Message{Content: f.text}, f.err
}
func (f *fake) ChatStream(ctx context.Context, m []Message, t []Tool, d func(string)) (Message, error) {
	return f.Chat(ctx, m, t)
}

func TestFallbackOnlyOnError(t *testing.T) {
	a, b := &fake{err: errors.New("down")}, &fake{text: "ok"}
	m, err := WithFallback{a, b}.Chat(context.Background(), nil, nil)
	if err != nil || m.Content != "ok" || a.n != 1 || b.n != 1 {
		t.Fatalf("%v %v %d %d", m, err, a.n, b.n)
	}
	a, b = &fake{text: "primary"}, &fake{text: "ok"}
	m, _ = WithFallback{a, b}.Chat(context.Background(), nil, nil)
	if m.Content != "primary" || b.n != 0 {
		t.Fatal("secondary used although the primary answered")
	}
}
