package notify

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestValidateTelegramChatID(t *testing.T) {
	for _, ok := range []string{"123456789", "-1001234567890", "44771"} {
		if err := ValidateTelegramChatID(ok); err != nil {
			t.Errorf("%s should pass: %v", ok, err)
		}
	}
	for _, bad := range []string{"", "abcdef", "@channelname", "+4915112345678", "12 34", "1", "-", strings.Repeat("9", 21)} {
		if err := ValidateTelegramChatID(bad); err == nil {
			t.Errorf("%q should fail", bad)
		}
	}
}

func TestTelegramSendsAndScrubsToken(t *testing.T) {
	var gotBody string
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/bottok-tg-secret/") {
			t.Errorf("path leaks nothing but must carry the token once: %s", r.URL.Path)
		}
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		w.WriteHeader(200)
		io.WriteString(w, `{"ok":true,"result":{"message_id":1}}`)
	}))
	defer fake.Close()
	t.Setenv("TELEGRAM_BOT_TOKEN", "tok-tg-secret")
	t.Setenv("TELEGRAM_API_BASE", fake.URL)
	t.Setenv("TELEGRAM_ALLOW_LOOPBACK", "1")
	n := &Notifier{allowLoopback: true}
	if err := n.Telegram(context.Background(), "-1001234567890", "[critical] boiler 2 above 90C"); err != nil {
		t.Fatalf("send: %v", err)
	}
	if !strings.Contains(gotBody, `"chat_id":"-1001234567890"`) || !strings.Contains(gotBody, "boiler 2") {
		t.Fatalf("body: %s", gotBody)
	}
}

func TestTelegramErrorNeverCarriesToken(t *testing.T) {
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(401)
		io.WriteString(w, `{"ok":false,"description":"Unauthorized for bottok-tg-secret"}`)
	}))
	defer fake.Close()
	t.Setenv("TELEGRAM_BOT_TOKEN", "tok-tg-secret")
	t.Setenv("TELEGRAM_API_BASE", fake.URL)
	t.Setenv("TELEGRAM_ALLOW_LOOPBACK", "1")
	n := &Notifier{allowLoopback: true}
	err := n.Telegram(context.Background(), "123456789", "hi")
	if err == nil || !strings.Contains(err.Error(), "401") {
		t.Fatalf("want 401 error, got %v", err)
	}
	if strings.Contains(err.Error(), "tok-tg-secret") {
		t.Fatalf("token leaked in error: %v", err)
	}
	t.Setenv("TELEGRAM_BOT_TOKEN", "")
	if err := n.Telegram(context.Background(), "123456789", "hi"); err == nil {
		t.Fatal("unconfigured should fail")
	}
}
