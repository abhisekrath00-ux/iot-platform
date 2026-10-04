package notify

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestTeamsCardAndValidation(t *testing.T) {
	var got map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		json.Unmarshal(b, &got)
		w.WriteHeader(200)
	}))
	defer srv.Close()
	n := &Notifier{allowLoopback: true}
	if err := n.Teams(context.Background(), srv.URL, "Alert", "Pump 3 over limit"); err != nil {
		t.Fatal(err)
	}
	if got["@type"] != "MessageCard" || got["text"] != "Pump 3 over limit" || got["title"] != "Alert" {
		t.Fatalf("card: %v", got)
	}
	// production rules: https only, no credentials, no loopback
	for _, bad := range []string{"http://hooks.example.com/x", "https://u:p@hooks.example.com/x", "ftp://x/y", ""} {
		if ValidateTeamsURL(bad) == nil {
			t.Errorf("%q should be rejected", bad)
		}
	}
	if ValidateTeamsURL("https://contoso.webhook.office.com/webhookb2/abc") != nil {
		t.Error("a normal https webhook should pass")
	}
	if err := (&Notifier{}).Teams(context.Background(), srv.URL, "a", "b"); err == nil {
		t.Fatal("plain http loopback must be refused in production")
	}
	// a long message is clipped, not rejected
	long := make([]rune, 9000)
	for i := range long {
		long[i] = 'x'
	}
	if err := n.Teams(context.Background(), srv.URL, "t", string(long)); err != nil || len([]rune(got["text"].(string))) != 4000 {
		t.Fatalf("clip: %v %d", err, len([]rune(got["text"].(string))))
	}
}

func TestSMSGateway(t *testing.T) {
	var auth, ct string
	var body map[string]string
	code := 200
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth, ct = r.Header.Get("Authorization"), r.Header.Get("Content-Type")
		b, _ := io.ReadAll(r.Body)
		json.Unmarshal(b, &body)
		w.WriteHeader(code)
	}))
	defer srv.Close()
	n := &Notifier{allowLoopback: true}
	t.Setenv("SMS_GATEWAY_URL", "")
	if SMSConfigured() || n.SMS(context.Background(), "+4915112345678", "x") == nil {
		t.Fatal("without a gateway nothing is sent")
	}
	t.Setenv("SMS_GATEWAY_URL", srv.URL+"/send")
	t.Setenv("SMS_GATEWAY_TOKEN", "tok123")
	if err := n.SMS(context.Background(), "+4915112345678", "Pump 3 over limit"); err != nil {
		t.Fatal(err)
	}
	if auth != "Bearer tok123" || ct != "application/json" || body["to"] != "+4915112345678" || body["message"] != "Pump 3 over limit" {
		t.Fatalf("request: %q %q %v", auth, ct, body)
	}
	if n.SMS(context.Background(), "0151 123", "x") == nil || n.SMS(context.Background(), "+1; DROP", "x") == nil {
		t.Fatal("bad numbers must be refused before any request")
	}
	code = 500
	if err := n.SMS(context.Background(), "+4915112345678", "x"); err == nil {
		t.Fatal("a gateway error must surface")
	}
	// a long text is cut to 480 characters
	code = 200
	long := make([]rune, 2000)
	for i := range long {
		long[i] = 'y'
	}
	n.SMS(context.Background(), "+4915112345678", string(long))
	if len([]rune(body["message"])) != 480 {
		t.Fatalf("sms length %d", len([]rune(body["message"])))
	}
	// loopback is refused unless the operator allows it
	if err := (&Notifier{}).SMS(context.Background(), "+4915112345678", "x"); err == nil {
		t.Fatal("loopback gateway must be refused by default")
	}
	t.Setenv("SMS_GATEWAY_ALLOW_LOOPBACK", "1")
	if err := (&Notifier{}).SMS(context.Background(), "+4915112345678", "x"); err != nil {
		t.Fatalf("operator-allowed loopback gateway: %v", err)
	}
}
