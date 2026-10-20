package notify

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSlackChannelsListsAndFlagsMembership(t *testing.T) {
	stub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/conversations.list") {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer xoxb-test" {
			t.Fatalf("missing bearer token")
		}
		w.Write([]byte(`{"ok":true,"channels":[{"id":"C0123","name":"site-alerts","is_member":true,"is_private":false},{"id":"G0456","name":"ops-private","is_member":true,"is_private":true}]}`))
	}))
	defer stub.Close()
	n := &Notifier{slackToken: "xoxb-test", slackBase: stub.URL, allowLoopback: true}
	chs, truncated, err := n.SlackChannels(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if truncated {
		t.Fatalf("single page must not be flagged truncated")
	}
	if len(chs) != 2 || chs[0].ID != "C0123" || !chs[0].IsMember || chs[1].ID != "G0456" || !chs[1].IsPrivate {
		t.Fatalf("unexpected channels %+v", chs)
	}
}

func TestSlackChannelsFollowsPaginationCursor(t *testing.T) {
	pages := map[string]string{
		"":      `{"ok":true,"channels":[{"id":"C01","name":"a","is_member":true}],"response_metadata":{"next_cursor":"page2"}}`,
		"page2": `{"ok":true,"channels":[{"id":"C02","name":"b","is_member":false,"is_private":true}],"response_metadata":{"next_cursor":""}}`,
	}
	var gotCursor []string
	stub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c := r.URL.Query().Get("cursor")
		gotCursor = append(gotCursor, c)
		body, ok := pages[c]
		if !ok {
			t.Fatalf("unexpected cursor %q", c)
		}
		w.Write([]byte(body))
	}))
	defer stub.Close()
	n := &Notifier{slackToken: "xoxb-test", slackBase: stub.URL, allowLoopback: true}
	chs, truncated, err := n.SlackChannels(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if truncated {
		t.Fatalf("two complete pages must not be flagged truncated")
	}
	if len(chs) != 2 || chs[0].ID != "C01" || chs[1].ID != "C02" {
		t.Fatalf("expected both pages merged, got %+v", chs)
	}
	if len(gotCursor) != 2 || gotCursor[1] != "page2" {
		t.Fatalf("expected the second request to carry next_cursor, got %v", gotCursor)
	}
}

func TestSlackChannelsCapsPaginationAndFlagsTruncation(t *testing.T) {
	calls := 0
	stub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Write([]byte(`{"ok":true,"channels":[{"id":"C01","name":"a","is_member":true}],"response_metadata":{"next_cursor":"more"}}`))
	}))
	defer stub.Close()
	n := &Notifier{slackToken: "xoxb-test", slackBase: stub.URL, allowLoopback: true}
	chs, truncated, err := n.SlackChannels(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !truncated {
		t.Fatalf("an endless cursor must be flagged truncated at the cap")
	}
	if calls != 10 || len(chs) != 10 {
		t.Fatalf("expected the 10-page cap, got %d calls / %d channels", calls, len(chs))
	}
}

func TestSlackChannelsSurfacesAPIError(t *testing.T) {
	stub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"ok":false,"error":"invalid_auth"}`)) // Slack reports failures as 200 + ok:false
	}))
	defer stub.Close()
	n := &Notifier{slackToken: "xoxb-bad", slackBase: stub.URL, allowLoopback: true}
	_, _, err := n.SlackChannels(context.Background())
	if err == nil || !strings.Contains(err.Error(), "invalid_auth") {
		t.Fatalf("expected invalid_auth, got %v", err)
	}
}

func TestSlackChannelsNeedsToken(t *testing.T) {
	n := &Notifier{}
	if _, _, err := n.SlackChannels(context.Background()); err == nil {
		t.Fatalf("expected not-configured error")
	}
}
