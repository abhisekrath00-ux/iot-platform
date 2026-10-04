package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
)

// Microsoft Teams and generic-HTTP SMS channels. Both are optional and need the internet only if the
// destination does; an on-prem site that sets neither never calls out. Both use the SSRF-safe client
// (loopback, link-local and metadata addresses refused at dial time, no redirects).

// ValidateTeamsURL is the admin-time check for a Teams channel target: an https incoming-webhook URL.
func ValidateTeamsURL(raw string) error {
	if err := ValidateWebhookURL(raw); err != nil {
		return errors.New("teams target must be an https incoming-webhook URL without credentials")
	}
	if u, _ := url.Parse(raw); u.Scheme != "https" {
		return errors.New("teams target must be an https incoming-webhook URL without credentials")
	}
	return nil
}

// Teams posts a message card to an incoming webhook. This is the classic connector format and the one
// Workflows webhooks that accept a "text" field understand; it has only been tested against a local
// receiver, not against Microsoft's service.
func (n *Notifier) Teams(ctx context.Context, target, title, text string) error {
	if err := ValidateTeamsURL(target); err != nil && !n.allowLoopback {
		return err
	}
	if n.allowLoopback { // tests use plain http on loopback
		if err := ValidateWebhookURL(target); err != nil {
			return err
		}
	}
	body, _ := json.Marshal(map[string]any{
		"@type": "MessageCard", "@context": "http://schema.org/extensions",
		"summary": clip(title, 120), "title": clip(title, 120), "text": clip(text, 4000),
	})
	req, err := http.NewRequestWithContext(ctx, "POST", target, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "hexmon-iot-teams/1")
	resp, err := safeClient(n.allowLoopback).Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return fmt.Errorf("teams: status %d", resp.StatusCode)
	}
	return nil
}

var phoneRe = regexp.MustCompile(`^\+[1-9][0-9]{6,14}$`)

// ValidatePhone checks an SMS channel target: an international number such as +4915112345678.
func ValidatePhone(p string) error {
	if !phoneRe.MatchString(p) {
		return errors.New("sms target must be an international number like +4915112345678")
	}
	return nil
}

// SMSConfigured reports whether the operator set a gateway. The gateway URL is operator configuration
// (SMS_GATEWAY_URL), never tenant input, so a tenant admin cannot point it at an internal address.
func SMSConfigured() bool { return os.Getenv("SMS_GATEWAY_URL") != "" }

// SMS sends one text through the operator's HTTP gateway: POST {"to": "+...", "message": "..."} with an
// optional bearer token (SMS_GATEWAY_TOKEN). Any 2xx is success. The gateway's own API shape is yours to
// bridge: this is the generic contract, not a Twilio/Vonage client.
func (n *Notifier) SMS(ctx context.Context, to, text string) error {
	gw := os.Getenv("SMS_GATEWAY_URL")
	if gw == "" {
		return errors.New("sms gateway not configured")
	}
	if err := ValidatePhone(to); err != nil {
		return err
	}
	if err := ValidateWebhookURL(gw); err != nil {
		return errors.New("SMS_GATEWAY_URL is not a valid http(s) URL")
	}
	body, _ := json.Marshal(map[string]string{"to": to, "message": clip(strings.TrimSpace(text), 480)})
	req, err := http.NewRequestWithContext(ctx, "POST", gw, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "hexmon-iot-sms/1")
	if tok := os.Getenv("SMS_GATEWAY_TOKEN"); tok != "" {
		req.Header.Set("Authorization", "Bearer "+tok)
	}
	resp, err := safeClient(n.allowLoopback || os.Getenv("SMS_GATEWAY_ALLOW_LOOPBACK") == "1").Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return fmt.Errorf("sms: status %d", resp.StatusCode)
	}
	return nil
}

func clip(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}
