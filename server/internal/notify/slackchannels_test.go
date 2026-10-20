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
	chs, err := n.SlackChannels(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(chs) != 2 || chs[0].ID != "C0123" || !chs[0].IsMember || chs[1].ID != "G0456" || !chs[1].IsPrivate {
		t.Fatalf("unexpected channels %+v", chs)
	}
}

func TestSlackChannelsSurfacesAPIError(t *testing.T) {
	stub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"ok":false,"error":"invalid_auth"}`)) // Slack reports failures as 200 + ok:false
	}))
	defer stub.Close()
	n := &Notifier{slackToken: "xoxb-bad", slackBase: stub.URL, allowLoopback: true}
	_, err := n.SlackChannels(context.Background())
	if err == nil || !strings.Contains(err.Error(), "invalid_auth") {
		t.Fatalf("expected invalid_auth, got %v", err)
	}
}

func TestSlackChannelsNeedsToken(t *testing.T) {
	n := &Notifier{}
	if _, err := n.SlackChannels(context.Background()); err == nil {
		t.Fatalf("expected not-configured error")
	}
}
