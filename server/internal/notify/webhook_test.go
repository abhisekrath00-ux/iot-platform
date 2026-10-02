package notify

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestValidateWebhookURL(t *testing.T) {
	for _, ok := range []string{"http://10.0.0.5:8080/hook", "https://hooks.example.com/x"} {
		if ValidateWebhookURL(ok) != nil {
			t.Errorf("%s should be valid", ok)
		}
	}
	for _, bad := range []string{"", "ftp://x/y", "file:///etc/passwd", "http://user:pw@host/", "http:///nohost", "javascript:alert(1)"} {
		if ValidateWebhookURL(bad) == nil {
			t.Errorf("%q should be rejected", bad)
		}
	}
}

func TestBlockedIP(t *testing.T) {
	for _, s := range []string{"127.0.0.1", "::1", "169.254.169.254", "0.0.0.0", "224.0.0.1", "fe80::1"} {
		if !blockedIP(net.ParseIP(s)) {
			t.Errorf("%s must be blocked", s)
		}
	}
	for _, s := range []string{"10.1.2.3", "192.168.0.9", "172.16.5.5", "8.8.8.8"} {
		if blockedIP(net.ParseIP(s)) {
			t.Errorf("%s must be allowed", s)
		}
	}
}

func TestWebhookRefusesLoopbackByDefault(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { t.Error("request reached a loopback server") }))
	defer srv.Close()
	if err := (&Notifier{}).Webhook(context.Background(), srv.URL, "x", nil); err == nil {
		t.Fatal("loopback target must be refused")
	}
}

func TestWebhookDeliversSignedJSONAndRefusesRedirect(t *testing.T) {
	t.Setenv("WEBHOOK_SIGNING_SECRET", "s3cret")
	var gotSig string
	var gotBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/redir" {
			http.Redirect(w, r, "http://169.254.169.254/", 302)
			return
		}
		gotSig = r.Header.Get("X-Hexmon-Signature")
		gotBody, _ = io.ReadAll(r.Body)
		w.WriteHeader(204)
	}))
	defer srv.Close()
	n := &Notifier{allowLoopback: true}
	if err := n.Webhook(context.Background(), srv.URL+"/ok", "alert.raised", map[string]any{"device": "d1"}); err != nil {
		t.Fatal(err)
	}
	m := hmac.New(sha256.New, []byte("s3cret"))
	m.Write(gotBody)
	if gotSig != "sha256="+hex.EncodeToString(m.Sum(nil)) {
		t.Fatalf("bad signature %q", gotSig)
	}
	if err := n.Webhook(context.Background(), srv.URL+"/redir", "x", nil); err == nil {
		t.Fatal("a 302 must not count as success or be followed")
	}
}

func TestHTTPDoRefusesLoopbackAndReadsBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		w.Write([]byte(r.Method + ":" + string(b)))
	}))
	defer srv.Close()
	if _, _, err := (&Notifier{}).HTTPDo(context.Background(), "GET", srv.URL, nil); err == nil {
		t.Fatal("loopback must be refused")
	}
	st, b, err := (&Notifier{allowLoopback: true}).HTTPDo(context.Background(), "POST", srv.URL, []byte("hi"))
	if err != nil || st != 200 || string(b) != "POST:hi" {
		t.Fatalf("%d %q %v", st, b, err)
	}
	if _, _, err := (&Notifier{allowLoopback: true}).HTTPDo(context.Background(), "DELETE", srv.URL, nil); err == nil {
		t.Error("DELETE accepted")
	}
}
