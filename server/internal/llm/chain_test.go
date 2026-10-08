package llm

import (
	"context"
	"errors"
	"testing"
)

type chainFake struct {
	name  string
	err   error
	calls int
}

func (f *chainFake) Name() string { return f.name }
func (f *chainFake) Chat(context.Context, []Message, []Tool) (Message, error) {
	f.calls++
	return Message{Content: f.name}, f.err
}
func (f *chainFake) ChatStream(_ context.Context, _ []Message, _ []Tool, on func(string)) (Message, error) {
	f.calls++
	if f.err == nil {
		on(f.name)
	}
	return Message{Content: f.name}, f.err
}

func TestFailoverable(t *testing.T) {
	for msg, want := range map[string]bool{
		"model endpoint returned 429: slow down":                          true,
		"model endpoint returned 401: bad key":                            true,
		"model endpoint returned 503: down":                               true,
		"model request failed: dial tcp: refused":                         true,
		"model endpoint returned 400: bad request":                        false,
		"model endpoint returned 400: exceeds the available context size": false,
		"something else": false,
	} {
		if got := Failoverable(errors.New(msg)); got != want {
			t.Errorf("%q: got %v want %v", msg, got, want)
		}
	}
	if Failoverable(context.Canceled) || Failoverable(nil) {
		t.Error("cancel/nil must not fail over")
	}
}

func TestChainFailsOverInOrderAndCoolsDown(t *testing.T) {
	ResetCooldowns()
	a := &chainFake{name: "a", err: errors.New("model endpoint returned 429: quota")}
	b := &chainFake{name: "b", err: errors.New("model request failed: refused")}
	c := &chainFake{name: "c"}
	ch := Chain{Providers: []Named{{"a", a}, {"b", b}, {"c", c}}}
	m, err := ch.Chat(context.Background(), nil, nil)
	if err != nil || m.Content != "c" || a.calls != 1 || b.calls != 1 || c.calls != 1 {
		t.Fatalf("got %v %v calls %d %d %d", m, err, a.calls, b.calls, c.calls)
	}
	// second request skips the two that just failed
	if m, _ = ch.Chat(context.Background(), nil, nil); m.Content != "c" || a.calls != 1 || b.calls != 1 {
		t.Fatalf("cool-down not applied: %d %d", a.calls, b.calls)
	}
}

func TestChainDoesNotFailOverOnBadRequestOrAnswer(t *testing.T) {
	ResetCooldowns()
	a := &chainFake{name: "a", err: errors.New("model endpoint returned 400: bad request")}
	b := &chainFake{name: "b"}
	if _, err := (Chain{Providers: []Named{{"a", a}, {"b", b}}}).Chat(context.Background(), nil, nil); err == nil || b.calls != 0 {
		t.Fatal("a 400 must not fail over")
	}
	ResetCooldowns()
	a2, b2 := &chainFake{name: "a"}, &chainFake{name: "b"}
	m, _ := (Chain{Providers: []Named{{"a", a2}, {"b", b2}}}).Chat(context.Background(), nil, nil)
	if m.Content != "a" || b2.calls != 0 {
		t.Fatal("an answer must not be retried")
	}
}

func TestChainAllFailReturnsLastErrorAndStillTriesPrimaryWhenCooling(t *testing.T) {
	ResetCooldowns()
	a := &chainFake{name: "a", err: errors.New("model endpoint returned 503: x")}
	b := &chainFake{name: "b", err: errors.New("model endpoint returned 503: y")}
	ch := Chain{Providers: []Named{{"a", a}, {"b", b}}}
	if _, err := ch.Chat(context.Background(), nil, nil); err == nil {
		t.Fatal("want error")
	}
	if _, err := ch.Chat(context.Background(), nil, nil); err == nil || a.calls != 2 {
		t.Fatalf("primary should be retried when all cooling, calls=%d", a.calls)
	}
}

func TestChainStreamFailsOverOnlyBeforeAnyText(t *testing.T) {
	ResetCooldowns()
	a := &chainFake{name: "a", err: errors.New("model endpoint returned 503: x")}
	b := &chainFake{name: "b"}
	var got string
	if _, err := (Chain{Providers: []Named{{"a", a}, {"b", b}}}).ChatStream(context.Background(), nil, nil, func(d string) { got += d }); err != nil || got != "b" {
		t.Fatalf("got %q %v", got, err)
	}
}
