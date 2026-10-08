package notify

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
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

func TestWhatsAppCloudAPI(t *testing.T) {
	var auth, path string
	var got map[string]any
	code, reply := 200, `{"messages":[{"id":"wamid.X"}]}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth, path = r.Header.Get("Authorization"), r.URL.Path
		b, _ := io.ReadAll(r.Body)
		got = nil
		json.Unmarshal(b, &got)
		w.WriteHeader(code)
		io.WriteString(w, reply)
	}))
	defer srv.Close()
	n := &Notifier{allowLoopback: true}
	if err := n.WhatsApp(context.Background(), "+919812345678", "x"); err == nil {
		t.Fatal("unconfigured must refuse")
	}
	t.Setenv("WHATSAPP_PHONE_NUMBER_ID", "1055")
	t.Setenv("WHATSAPP_TOKEN", "tok-secret")
	t.Setenv("WHATSAPP_API_BASE", srv.URL)
	if !WhatsAppConfigured() {
		t.Fatal("configured")
	}
	// plain text
	if err := n.WhatsApp(context.Background(), "+919812345678", "Pump 3 over limit"); err != nil {
		t.Fatal(err)
	}
	if path != "/1055/messages" || auth != "Bearer tok-secret" || got["to"] != "919812345678" || got["type"] != "text" ||
		got["text"].(map[string]any)["body"] != "Pump 3 over limit" || got["messaging_product"] != "whatsapp" {
		t.Fatalf("text: %s %s %v", path, auth, got)
	}
	// template: one line, one variable
	t.Setenv("WHATSAPP_TEMPLATE", "hex_alert")
	if err := n.WhatsApp(context.Background(), "+919812345678", "[critical]\n  Pump 3\tover   limit"); err != nil {
		t.Fatal(err)
	}
	tpl := got["template"].(map[string]any)
	p := tpl["components"].([]any)[0].(map[string]any)["parameters"].([]any)[0].(map[string]any)
	if got["type"] != "template" || tpl["name"] != "hex_alert" || tpl["language"].(map[string]any)["code"] != "en" || p["text"] != "[critical] Pump 3 over limit" {
		t.Fatalf("template: %v", got)
	}
	// errors: provider message surfaces, token never does
	code, reply = 401, `{"error":{"message":"Invalid token tok-secret","code":190}}`
	err := n.WhatsApp(context.Background(), "+919812345678", "x")
	if err == nil || strings.Contains(err.Error(), "tok-secret") || !strings.Contains(err.Error(), "401") {
		t.Fatalf("error: %v", err)
	}
	if ValidateWhatsAppNumber("9812345678") == nil || ValidateWhatsAppNumber("+919812345678") != nil {
		t.Fatal("number validation")
	}
	// production client refuses a loopback gateway
	if err := (&Notifier{}).WhatsApp(context.Background(), "+919812345678", "x"); err == nil {
		t.Fatal("loopback must be refused in production")
	}
}
