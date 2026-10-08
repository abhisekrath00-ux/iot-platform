package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"regexp"
	"strings"
)

// WhatsApp alert channel through the WhatsApp Business Cloud API. Optional and off unless the operator
// sets WHATSAPP_PHONE_NUMBER_ID and WHATSAPP_TOKEN (operator configuration, never tenant input, so a
// tenant admin cannot point it at another host). It needs the internet and a Meta business account; an
// air-gapped site simply leaves it unset. WHATSAPP_API_BASE may point at a Cloud API compatible gateway.
//
// WhatsApp only lets a business start a conversation with an approved message template. Set
// WHATSAPP_TEMPLATE (and WHATSAPP_TEMPLATE_LANG, default en) to a template whose body has one {{1}}
// variable. Without a template the message goes out as plain text, which WhatsApp delivers only to
// people who wrote to the business number in the last 24 hours.

const defaultWhatsAppBase = "https://graph.facebook.com/v20.0"

var waNumberRe = regexp.MustCompile(`^\+[1-9][0-9]{6,14}$`)

// ValidateWhatsAppNumber checks a channel target: an international number such as +919812345678.
func ValidateWhatsAppNumber(p string) error {
	if !waNumberRe.MatchString(p) {
		return errors.New("whatsapp target must be an international number like +919812345678")
	}
	return nil
}

// WhatsAppConfigured reports whether the operator set the account and token.
func WhatsAppConfigured() bool {
	return os.Getenv("WHATSAPP_PHONE_NUMBER_ID") != "" && os.Getenv("WHATSAPP_TOKEN") != ""
}

var waSpaceRe = regexp.MustCompile(`\s+`)

// WhatsApp sends one alert. Any 2xx is success. The token is never put in an error message.
func (n *Notifier) WhatsApp(ctx context.Context, to, text string) error {
	id, tok := os.Getenv("WHATSAPP_PHONE_NUMBER_ID"), os.Getenv("WHATSAPP_TOKEN")
	if id == "" || tok == "" {
		return errors.New("whatsapp is not configured")
	}
	if err := ValidateWhatsAppNumber(to); err != nil {
		return err
	}
	base := strings.TrimRight(envOr("WHATSAPP_API_BASE", defaultWhatsAppBase), "/")
	if err := ValidateWebhookURL(base); err != nil {
		return errors.New("WHATSAPP_API_BASE is not a valid http(s) URL")
	}
	msg := map[string]any{"messaging_product": "whatsapp", "to": strings.TrimPrefix(to, "+")}
	if tpl := os.Getenv("WHATSAPP_TEMPLATE"); tpl != "" {
		// Template variables cannot hold new lines or tabs, so the alert goes in as one line.
		one := clip(strings.TrimSpace(waSpaceRe.ReplaceAllString(text, " ")), 1000)
		msg["type"] = "template"
		msg["template"] = map[string]any{
			"name":     tpl,
			"language": map[string]string{"code": envOr("WHATSAPP_TEMPLATE_LANG", "en")},
			"components": []any{map[string]any{"type": "body",
				"parameters": []any{map[string]string{"type": "text", "text": one}}}},
		}
	} else {
		msg["type"] = "text"
		msg["text"] = map[string]any{"body": clip(strings.TrimSpace(text), 4000), "preview_url": false}
	}
	body, _ := json.Marshal(msg)
	req, err := http.NewRequestWithContext(ctx, "POST", base+"/"+id+"/messages", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("User-Agent", "hexmon-iot-whatsapp/1")
	resp, err := safeClient(n.allowLoopback || os.Getenv("WHATSAPP_ALLOW_LOOPBACK") == "1").Do(req)
	if err != nil {
		return errors.New("whatsapp: request failed")
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 600))
		var e struct {
			Error struct {
				Message string `json:"message"`
				Code    int    `json:"code"`
			} `json:"error"`
		}
		_ = json.Unmarshal(b, &e)
		if e.Error.Message != "" {
			return fmt.Errorf("whatsapp: status %d: %s", resp.StatusCode, clip(strings.ReplaceAll(e.Error.Message, tok, "***"), 200))
		}
		return fmt.Errorf("whatsapp: status %d", resp.StatusCode)
	}
	return nil
}
