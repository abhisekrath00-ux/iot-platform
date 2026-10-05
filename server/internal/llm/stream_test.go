package llm

import (
	"strings"
	"testing"
)

func TestReadStreamAssemblesTextAndToolCalls(t *testing.T) {
	in := `data: {"choices":[{"delta":{"content":"Hel"}}]}

data: {"choices":[{"delta":{"content":"lo"}}]}

data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"a1","function":{"name":"list_","arguments":"{\"x\""}}]}}]}

data: {"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"name":"devices","arguments":":1}"}}]}}]}

data: [DONE]
`
	var got []string
	m, err := readStream(strings.NewReader(in), func(s string) { got = append(got, s) })
	if err != nil {
		t.Fatal(err)
	}
	if m.Content != "Hello" || strings.Join(got, "|") != "Hel|lo" {
		t.Fatalf("text %q deltas %v", m.Content, got)
	}
	if len(m.ToolCalls) != 1 || m.ToolCalls[0].ID != "a1" || m.ToolCalls[0].Func.Name != "list_devices" || m.ToolCalls[0].Func.Arguments != `{"x":1}` {
		t.Fatalf("tool calls %+v", m.ToolCalls)
	}
}

func TestReadStreamRejectsNonStream(t *testing.T) {
	if _, err := readStream(strings.NewReader(`{"error":"nope"}`), nil); err == nil {
		t.Fatal("a body that is not an event stream must be an error")
	}
}

func TestBodyOnlyAddsThinkingFlagWhenAsked(t *testing.T) {
	if _, ok := buildBody(Config{}, nil, nil, false)["chat_template_kwargs"]; ok {
		t.Fatal("hosted endpoints must not get unknown fields")
	}
	if _, ok := buildBody(Config{NoThinking: true}, nil, nil, true)["chat_template_kwargs"]; !ok {
		t.Fatal("local runtime flag missing")
	}
}
