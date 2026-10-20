package notify

import (
	"context"
	"fmt"
	"net/smtp"
	"strings"
)

// EmailHTML sends a multipart/alternative message: a plain-text part for clients
// that cannot or will not render HTML, then the HTML part. Use it only for bodies
// that are already safe HTML (escaped at construction, e.g. report renders);
// plain notifications keep using Email. The report scheduler's raw-markup defect
// (HTML source inside a text/plain message) is what this fixes.
func (n *Notifier) EmailHTML(ctx context.Context, to []string, subject, plainText, htmlBody string) error {
	if n.smtpHost == "" || n.from == "" {
		return fmt.Errorf("smtp not configured")
	}
	for _, h := range append([]string{subject, n.from}, to...) {
		if strings.ContainsAny(h, "\r\n") {
			return fmt.Errorf("invalid mail header")
		}
	}
	if len(to) == 0 {
		return fmt.Errorf("no recipient")
	}
	// A boundary that cannot collide with either part's content.
	boundary := "hx-boundary-1"
	for i := 2; strings.Contains(plainText, boundary) || strings.Contains(htmlBody, boundary); i++ {
		boundary = fmt.Sprintf("hx-boundary-%d", i)
	}
	var msg strings.Builder
	msg.WriteString("From: " + n.from + "\r\nTo: " + strings.Join(to, ", ") + "\r\nSubject: " + subject +
		"\r\nMIME-Version: 1.0\r\nContent-Type: multipart/alternative; boundary=\"" + boundary + "\"\r\n\r\n")
	msg.WriteString("--" + boundary + "\r\nContent-Type: text/plain; charset=utf-8\r\n\r\n" + plainText + "\r\n")
	msg.WriteString("\r\n--" + boundary + "\r\nContent-Type: text/html; charset=utf-8\r\n\r\n" + htmlBody + "\r\n")
	msg.WriteString("\r\n--" + boundary + "--\r\n")
	var auth smtp.Auth
	if n.smtpUser != "" {
		auth = smtp.PlainAuth("", n.smtpUser, n.smtpPass, n.smtpHost)
	}
	return smtp.SendMail(n.smtpHost+":"+n.smtpPort, auth, n.from, to, []byte(msg.String()))
}
