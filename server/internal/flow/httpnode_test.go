package flow

import (
	"context"
	"errors"
	"strings"
	"testing"
)

type fakeHTTP struct {
	status int
	body   string
	err    error
	calls  int
	gotURL string
	gotReq string
}

func (f *fakeHTTP) HTTPDo(_ context.Context, method, url string, body []byte) (int, []byte, error) {
	f.calls++
	f.gotURL, f.gotReq = method+" "+url, string(body)
	return f.status, []byte(f.body), f.err
}

func httpFlow(extract string) Definition {
	g := Graph{
		Nodes: []Node{
			trig(),
			{ID: "h", Type: "http", Method: "POST", URL: "http://10.0.0.7/api/forecast", Body: `{"v":{value}}`, Target: "temp", Extract: extract},
			{ID: "hot", Type: "condition", Op: ">", Value: 30},
			{ID: "ok", Type: "notify", ChannelID: "ok", Message: "outside {vars.temp} ({vars.temp_status})"},
			{ID: "bad", Type: "notify", ChannelID: "bad", Message: "lookup failed"},
		},
		Edges: []Edge{{From: "t", To: "h"}, {From: "h", Port: "0", To: "ok"}, {From: "h", Port: "1", To: "bad"}},
	}
	_ = g.Nodes[2]
	g.Nodes = append(g.Nodes[:2], g.Nodes[3:]...)
	return Definition{Graph: &g}
}

func TestHTTPNodeStoresResponseAndRoutes(t *testing.T) {
	d := httpFlow("main.temp")
	if err := Validate(d); err != nil {
		t.Fatal(err)
	}
	f := &fakeHTTP{status: 200, body: `{"main":{"temp":31.5}}`}
	r := d.Exec(20, "d", "p", ExecOptions{HTTP: f})
	if len(r.Actions) != 1 || r.Actions[0].ChannelID != "ok" || r.Actions[0].Message != "outside 31.5 (200)" {
		t.Fatalf("%+v %+v", r.Actions, r.Debug)
	}
	if f.gotURL != "POST http://10.0.0.7/api/forecast" || f.gotReq != `{"v":20}` {
		t.Fatalf("%q %q", f.gotURL, f.gotReq)
	}
	for name, ff := range map[string]*fakeHTTP{
		"500":       {status: 500, body: "x"},
		"net error": {err: errors.New("refused")},
		"not json":  {status: 200, body: "<html>"},
		"no path":   {status: 200, body: `{"main":{}}`},
		"object":    {status: 200, body: `{"main":{"temp":{"a":1}}}`},
	} {
		r := d.Exec(20, "d", "p", ExecOptions{HTTP: ff})
		if len(r.Actions) != 1 || r.Actions[0].ChannelID != "bad" {
			t.Errorf("%s: %+v", name, r.Actions)
		}
	}
}

func TestHTTPNodeDisabledAndLimited(t *testing.T) {
	d := httpFlow("main.temp")
	r := d.Exec(20, "d", "p", ExecOptions{}) // no HTTP client: dry run / feature off
	if len(r.Actions) != 1 || r.Actions[0].ChannelID != "bad" || len(r.Debug) == 0 || !strings.Contains(r.Debug[0].Message, "disabled") {
		t.Fatalf("%+v", r)
	}
	// three http nodes in a chain: the third is skipped by the per-run cap
	g := Graph{
		Nodes: []Node{trig(),
			{ID: "h1", Type: "http", Method: "GET", URL: "http://10.0.0.7/a", Target: "a"},
			{ID: "h2", Type: "http", Method: "GET", URL: "http://10.0.0.7/b", Target: "b"},
			{ID: "h3", Type: "http", Method: "GET", URL: "http://10.0.0.7/c", Target: "c"},
			{ID: "n", Type: "notify", ChannelID: "n"}, {ID: "x", Type: "notify", ChannelID: "x"}},
		Edges: []Edge{{From: "t", To: "h1"}, {From: "h1", To: "h2"}, {From: "h2", To: "h3"}, {From: "h3", Port: "0", To: "n"}, {From: "h3", Port: "1", To: "x"}},
	}
	f := &fakeHTTP{status: 200, body: "1"}
	dd := Definition{Graph: &g}
	if err := Validate(dd); err != nil {
		t.Fatal(err)
	}
	r = dd.Exec(20, "d", "p", ExecOptions{HTTP: f})
	if f.calls != maxHTTPPerRun || len(r.Actions) != 1 || r.Actions[0].ChannelID != "x" {
		t.Fatalf("calls %d %+v", f.calls, r.Actions)
	}
}

func TestHTTPNodeValidation(t *testing.T) {
	base := Node{ID: "h", Type: "http", Method: "GET", URL: "http://10.0.0.7/x", Target: "t"}
	ok := base
	if validateNode(&ok) != nil {
		t.Fatal("good node rejected")
	}
	for name, mut := range map[string]func(*Node){
		"method":       func(n *Node) { n.Method = "DELETE" },
		"ftp":          func(n *Node) { n.URL = "ftp://x/y" },
		"creds":        func(n *Node) { n.URL = "http://u:p@host/" },
		"placeholder":  func(n *Node) { n.URL = "http://{vars.host}/x" },
		"space":        func(n *Node) { n.URL = "http://a b/" },
		"get body":     func(n *Node) { n.Body = "x" },
		"target":       func(n *Node) { n.Target = "bad name" },
		"extract":      func(n *Node) { n.Extract = "a;b" },
		"empty target": func(n *Node) { n.Target = "" },
	} {
		n := base
		mut(&n)
		if validateNode(&n) == nil {
			t.Errorf("%s accepted", name)
		}
	}
	if _, err := httpResult([]byte(strings.Repeat("a", 300)), ""); err == nil {
		t.Error("oversized value accepted")
	}
}
