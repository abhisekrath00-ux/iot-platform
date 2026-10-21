package googlews

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net/http"
	"strings"
	"unicode/utf8"
)

// GmailSender sends mail through the Gmail API with the tenant's OAuth grant
// (gmail.send scope) instead of an SMTP relay. The endpoint is injectable so
// tests run against a local simulator; nothing here dials Google by itself.
type GmailSender struct {
	p        *Provider
	s        SecretStore
	GmailURL string // default https://gmail.googleapis.com/gmail/v1/users/me/messages/send
}

func NewGmailSender(p *Provider, s SecretStore) *GmailSender {
	return &GmailSender{p: p, s: s, GmailURL: "https://gmail.googleapis.com/gmail/v1/users/me/messages/send"}
}

// Send delivers one plain-text message as the connected Google account.
// to/subject/body come from the platform's own notification pipeline, not
// from the Google grant. Returns the Gmail message id.
func (g *GmailSender) Send(ctx context.Context, tenant, to, subject, body string) (string, error) {
	if strings.TrimSpace(to) == "" || !strings.Contains(to, "@") || strings.ContainsAny(to, "\r\n\t ,;") {
		return "", fmt.Errorf("googlews gmail: bad recipient %q", to)
	}
	if err := RequireScope(ctx, g.s, tenant, ScopeGmailSend); err != nil {
		return "", err
	}
	at, err := AccessToken(ctx, g.p, g.s, tenant)
	if err != nil {
		return "", err
	}
	raw := buildRFC2822(to, subject, body)
	payload, _ := json.Marshal(map[string]string{"raw": base64.RawURLEncoding.EncodeToString([]byte(raw))})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, g.GmailURL, strings.NewReader(string(payload)))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+at)
	resp, err := g.p.http.Do(req)
	if err != nil {
		return "", fmt.Errorf("gmail send: %w", err)
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", err
	}
	if resp.StatusCode != 200 {
		var ge struct {
			Error struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		_ = json.Unmarshal(b, &ge)
		return "", fmt.Errorf("gmail send: %s (%s)", resp.Status, ge.Error.Message)
	}
	var out struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(b, &out); err != nil {
		return "", fmt.Errorf("gmail send: bad response: %w", err)
	}
	return out.ID, nil
}

// buildRFC2822 renders a minimal CRLF plain-text message. Subject is
// RFC 2047 encoded-word when non-ASCII; headers are stripped of CR/LF so a
// value can never inject extra headers.
func buildRFC2822(to, subject, body string) string {
	clean := func(s string) string {
		return strings.Map(func(r rune) rune {
			if r == '\r' || r == '\n' {
				return -1
			}
			return r
		}, s)
	}
	subj := clean(subject)
	if !isASCII(subj) {
		subj = mime.QEncoding.Encode("utf-8", subj)
	}
	if !utf8.ValidString(body) {
		body = strings.ToValidUTF8(body, "")
	}
	return "To: " + clean(to) + "\r\nSubject: " + subj +
		"\r\nContent-Type: text/plain; charset=utf-8\r\n\r\n" + body + "\r\n"
}

func isASCII(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] >= 0x80 {
			return false
		}
	}
	return true
}
