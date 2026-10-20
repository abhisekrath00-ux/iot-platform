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
	"net/url"
	"os"
	"strings"
)

type Notifier struct {
	smtpHost, smtpPort, smtpUser, smtpPass, from string
	slackToken, slackBase                        string
	allowLoopback                                bool // tests only
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
	// header values must not carry line breaks (header injection)
	for _, h := range append([]string{subject, n.from}, to...) {
		if strings.ContainsAny(h, "\r\n") {
			return fmt.Errorf("invalid mail header")
		}
	}
	if len(to) == 0 {
		return fmt.Errorf("no recipient")
	}
	msg := []byte("From: " + n.from + "\r\nTo: " + strings.Join(to, ", ") + "\r\nSubject: " + subject +
		"\r\nMIME-Version: 1.0\r\nContent-Type: text/plain; charset=utf-8\r\n\r\n" + body + "\r\n")
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

// SlackChannel is one workspace channel the bot token can see.
type SlackChannel struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	IsMember  bool   `json:"is_member"`
	IsPrivate bool   `json:"is_private"`
}

// SlackChannels lists the workspace channels the bot token can see (conversations.list), so the
// settings page can offer a picker instead of making the operator copy a channel ID by hand.
// Slack answers application-level failures with HTTP 200 and ok:false, so check ok, not the status,
// and pages results via response_metadata.next_cursor - follow it or the list silently truncates at
// one page. Pagination is capped at 10 pages of 200 (2000 channels); truncated is true when Slack
// still had a next_cursor at the cap.
func (n *Notifier) SlackChannels(ctx context.Context) (channels []SlackChannel, truncated bool, err error) {
	if n.slackToken == "" {
		return nil, false, fmt.Errorf("slack not configured")
	}
	cursor := ""
	for page := 0; page < 10; page++ {
		u := n.slackBase + "/conversations.list?types=public_channel,private_channel&exclude_archived=true&limit=200"
		if cursor != "" {
			u += "&cursor=" + url.QueryEscape(cursor)
		}
		req, _ := http.NewRequestWithContext(ctx, "GET", u, nil)
		req.Header.Set("Authorization", "Bearer "+n.slackToken)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return nil, false, err
		}
		var out struct {
			OK       bool           `json:"ok"`
			Error    string         `json:"error"`
			Channels []SlackChannel `json:"channels"`
			Meta     struct {
				NextCursor string `json:"next_cursor"`
			} `json:"response_metadata"`
		}
		derr := json.NewDecoder(resp.Body).Decode(&out)
		resp.Body.Close()
		if derr != nil {
			return nil, false, derr
		}
		if !out.OK {
			return nil, false, fmt.Errorf("slack: %s", out.Error)
		}
		channels = append(channels, out.Channels...)
		if out.Meta.NextCursor == "" {
			return channels, false, nil
		}
		cursor = out.Meta.NextCursor
	}
	return channels, true, nil
}

// SlackConfigured reports whether a bot token is set (SLACK_BOT_TOKEN).
func SlackConfigured() bool { return os.Getenv("SLACK_BOT_TOKEN") != "" }

// Configured reports whether outgoing mail is set up (SMTP_HOST and ALERT_FROM_EMAIL). Mail is optional and
// off by default: an air-gapped deployment with no relay simply has no email features.
func (n *Notifier) Configured() bool { return n.smtpHost != "" && n.from != "" }

func envOr(k, d string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return d
}
