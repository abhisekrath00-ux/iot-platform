package ingestprocessing

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"
)

// Slack is a deliberately separate one-attempt adapter for the DEFAULT-OFF
// processing engine, not a replacement for notify.Notifier or startup wiring.
// It accepts only exact channel IDs, never names or user IDs that open a DM.
// A nil result proves a bounded provider acceptance response, not delivery.
// No retries, redirects, rate-limit sleeps, token discovery or channel lookup.
var (
	ErrSlackInput   = errors.New("staged Slack sender requires an exact channel ID and bounded nonempty text")
	ErrSlackOutcome = errors.New("staged Slack provider acceptance not established; do not retry")
	slackChannelID  = regexp.MustCompile(`^[CGD][A-Z0-9]{5,63}$`)
	slackTimestamp  = regexp.MustCompile(`^[0-9]{1,20}\.[0-9]{6}$`)
)

type slackSender struct {
	token    string
	endpoint string
	client   *http.Client
}

// NewSlackSender requires an explicit credential and uses only the official
// HTTPS method. It does NOT read environment, activate processing, validate a
// credential remotely, or authorize an effect. ExecuteOnce owns retained policy
// validation/claim and batch quarantine. Existing production callers are unchanged.
func NewSlackSender(token string) (Sender, error) {
	if token == "" || len(token) > 4096 || strings.IndexFunc(token, func(r rune) bool { return r <= 32 || r >= 127 }) >= 0 {
		return nil, errors.New("invalid staged Slack credential")
	}
	tr := &http.Transport{
		Proxy:                 nil, // Do not inherit an ambient proxy that could receive the token.
		DialContext:           (&net.Dialer{Timeout: 3 * time.Second}).DialContext,
		TLSClientConfig:       &tls.Config{MinVersion: tls.VersionTLS12},
		TLSHandshakeTimeout:   3 * time.Second,
		ResponseHeaderTimeout: 5 * time.Second,
		DisableKeepAlives:     true, // No reused-connection retry path.
		ForceAttemptHTTP2:     false,
	}
	return &slackSender{token: token, endpoint: "https://slack.com/api/chat.postMessage", client: &http.Client{
		Transport: tr, Timeout: 10 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}}, nil
}

func validSlackIntent(intent Intent) error {
	if intent.ChannelType != "slack" || !slackChannelID.MatchString(intent.Target) || !utf8.ValidString(intent.Message) || strings.TrimSpace(intent.Message) == "" || utf8.RuneCountInString(intent.Message) > 4000 || strings.IndexByte(intent.Message, 0) >= 0 || strings.Contains(intent.Message, "<@") || strings.Contains(intent.Message, "<!") || strings.Contains(intent.Message, "<#") {
		return ErrSlackInput
	}
	return nil
}
func (s *slackSender) validateIntents(intents []Intent) error {
	if s == nil || s.client == nil || s.token == "" {
		return ErrSenderUnavailable
	}
	for _, intent := range intents {
		if err := validSlackIntent(intent); err != nil {
			return err
		}
	}
	return nil
}
func (s *slackSender) SendOnce(ctx context.Context, _ Envelope, intent Intent) error {
	if err := s.validateIntents([]Intent{intent}); err != nil {
		return err
	}
	if ctx.Err() != nil {
		return ErrSlackOutcome
	}
	// Refuse explicit mention/channel markup and suppress automatic expansion
	// and URL/media unfurls. Do not silently add an
	// audience, thread, identity override, attachment, or request for an external URL.
	b, err := json.Marshal(map[string]any{"channel": intent.Target, "text": intent.Message, "mrkdwn": false, "parse": "none", "link_names": false, "unfurl_links": false, "unfurl_media": false})
	if err != nil {
		return ErrSlackInput
	}
	c, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(c, http.MethodPost, s.endpoint, bytes.NewReader(b))
	if err != nil {
		return ErrSlackOutcome
	}
	// A POST with no idempotency headers is not replayable by net/http. Clear the
	// body recreation callback as an additional safeguard against future changes.
	req.GetBody = nil
	req.Header.Set("Authorization", "Bearer "+s.token)
	req.Header.Set("Content-Type", "application/json; charset=utf-8")
	resp, err := s.client.Do(req)
	if err != nil {
		return ErrSlackOutcome
	} // Never include token, URL or provider body.
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return ErrSlackOutcome
	}
	const limit = 65536
	raw, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil || len(raw) > limit {
		return ErrSlackOutcome
	}
	// Check the complete bounded JSON value, not just HTTP 200 or its first object.
	var out struct {
		OK               bool   `json:"ok"`
		Channel          string `json:"channel"`
		TS               string `json:"ts"`
		Warning          string `json:"warning"`
		ResponseMetadata struct {
			Warnings []string `json:"warnings"`
		} `json:"response_metadata"`
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil || fields["ok"] == nil || fields["channel"] == nil || fields["ts"] == nil || !uniqueJSON(raw) || json.Unmarshal(raw, &out) != nil || !out.OK || out.Channel != intent.Target || !slackTimestamp.MatchString(out.TS) || out.Warning != "" || len(out.ResponseMetadata.Warnings) > 0 || c.Err() != nil {
		return ErrSlackOutcome
	}
	return nil
}

// Reject duplicate keys at every level instead of allowing last-key-wins to
// turn an ambiguous provider result into acceptance. Also bound nesting depth.
func uniqueJSON(raw []byte) bool {
	d := json.NewDecoder(bytes.NewReader(raw))
	var value func(int) bool
	value = func(depth int) bool {
		if depth > 32 {
			return false
		}
		tok, err := d.Token()
		if err != nil {
			return false
		}
		delim, ok := tok.(json.Delim)
		if !ok {
			return true
		}
		switch delim {
		case '{':
			seen := map[string]bool{}
			for d.More() {
				k, err := d.Token()
				if err != nil {
					return false
				}
				key, ok := k.(string)
				key = strings.ToLower(key)
				if !ok || seen[key] {
					return false
				}
				seen[key] = true
				if !value(depth + 1) {
					return false
				}
			}
			end, err := d.Token()
			return err == nil && end == json.Delim('}')
		case '[':
			for d.More() {
				if !value(depth + 1) {
					return false
				}
			}
			end, err := d.Token()
			return err == nil && end == json.Delim(']')
		}
		return false
	}
	if !value(0) {
		return false
	}
	_, err := d.Token()
	return err == io.EOF
}
