// Package notify sends alert notifications over customer-configured channels:
// SMTP (their mail server) and Slack. Channel config is per-tenant.
package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/smtp"
	"os"
)

type Notifier struct {
	smtpHost, smtpPort, smtpUser, smtpPass, from string
	slackToken, slackBase                        string
}

func FromEnv() *Notifier {
	return &Notifier{
		smtpHost: os.Getenv("SMTP_HOST"), smtpPort: envOr("SMTP_PORT", "587"),
		smtpUser: os.Getenv("SMTP_USER"), smtpPass: os.Getenv("SMTP_PASS"),
		from:       os.Getenv("ALERT_FROM_EMAIL"),
		slackToken: os.Getenv("SLACK_BOT_TOKEN"),
		// Overridable for air-gapped relays (e.g. Mattermost-compatible bridge).
		slackBase: envOr("SLACK_API_BASE", "https://slack.com/api"),
	}
}

// Email sends via the tenant's SMTP server. No-op (logged by caller) when
// SMTP is not configured.
func (n *Notifier) Email(ctx context.Context, to []string, subject, body string) error {
	if n.smtpHost == "" || n.from == "" {
		return fmt.Errorf("smtp not configured")
	}
	msg := []byte("From: " + n.from + "\r\nSubject: " + subject + "\r\n\r\n" + body + "\r\n")
	var auth smtp.Auth
	if n.smtpUser != "" {
		auth = smtp.PlainAuth("", n.smtpUser, n.smtpPass, n.smtpHost)
	}
	return smtp.SendMail(n.smtpHost+":"+n.smtpPort, auth, n.from, to, msg)
}

// Slack posts to a channel via a bot token (chat.postMessage).
func (n *Notifier) Slack(ctx context.Context, channel, text string) error {
	if n.slackToken == "" {
		return fmt.Errorf("slack not configured")
	}
	payload, _ := json.Marshal(map[string]string{"channel": channel, "text": text})
	req, _ := http.NewRequestWithContext(ctx, "POST", n.slackBase+"/chat.postMessage", bytes.NewReader(payload))
	req.Header.Set("Authorization", "Bearer "+n.slackToken)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("slack: status %d", resp.StatusCode)
	}
	return nil
}

func envOr(k, d string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return d
}
