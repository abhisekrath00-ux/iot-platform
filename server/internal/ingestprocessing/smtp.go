package ingestprocessing

import (
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"io"
	"mime/quotedprintable"
	"net"
	"net/mail"
	"net/smtp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

var (
	ErrSMTPInput   = errors.New("invalid staged SMTP configuration, recipient or bounded text")
	ErrSMTPOutcome = errors.New("staged SMTP relay acceptance not established; do not retry")
)

// SMTPConfig is explicit, not loaded from production environment. Mode must be
// starttls or implicit. There is no plaintext, opportunistic TLS or insecure
// certificate option. Empty Username/Password explicitly selects a TLS relay
// without AUTH; otherwise both are required. No token/account discovery.
type SMTPConfig struct {
	Host                           string
	Port                           int
	Mode, From, Username, Password string
}
type smtpSender struct {
	config SMTPConfig
	tls    *tls.Config
}

// NewSMTPSender is for the disabled processing path only. It does not connect,
// activate workers, replace notify.Notifier or authorize sending any message.
func NewSMTPSender(c SMTPConfig) (Sender, error) {
	if c.Host == "" || len(c.Host) > 253 || strings.ContainsAny(c.Host, " \t\r\n/@[]") || strings.IndexFunc(c.Host, func(r rune) bool { return r <= 32 || r >= 127 }) >= 0 || c.Port < 1 || c.Port > 65535 || (c.Mode != "implicit" && c.Mode != "starttls") || !exactMailbox(c.From) || (c.Username == "") != (c.Password == "") || len(c.Username) > 1024 || len(c.Password) > 4096 || strings.ContainsAny(c.Username+c.Password, "\x00\r\n") {
		return nil, ErrSMTPInput
	}
	return &smtpSender{config: c, tls: &tls.Config{ServerName: c.Host, MinVersion: tls.VersionTLS12}}, nil
}
func exactMailbox(s string) bool {
	if len(s) > 254 || strings.IndexFunc(s, func(r rune) bool { return r <= 32 || r >= 127 }) >= 0 {
		return false
	}
	a, err := mail.ParseAddress(s)
	return err == nil && a.Name == "" && a.Address == s && strings.Count(s, "@") == 1
}

// smtpWireBudget caps inbound SMTP/TLS bytes over the entire connection, so a
// pathological multiline greeting/reply cannot cause unbounded accumulation.
// All network operations also share the total deadline and cancellation close.
type smtpWireBudget struct {
	net.Conn
	left int64
}

func (c *smtpWireBudget) Read(p []byte) (int, error) {
	if c.left <= 0 {
		return 0, io.ErrUnexpectedEOF
	}
	if int64(len(p)) > c.left {
		p = p[:c.left]
	}
	n, err := c.Conn.Read(p)
	c.left -= int64(n)
	return n, err
}

func validSMTPIntent(i Intent) error {
	if i.ChannelType != "email" || !exactMailbox(i.Target) || !utf8.ValidString(i.Message) || strings.TrimSpace(i.Message) == "" || len(i.Message) > 65536 || strings.IndexByte(i.Message, 0) >= 0 {
		return ErrSMTPInput
	}
	return nil
}
func (s *smtpSender) validateIntents(intents []Intent) error {
	if s == nil || s.tls == nil {
		return ErrSenderUnavailable
	}
	for _, intent := range intents {
		if err := validSMTPIntent(intent); err != nil {
			return err
		}
	}
	return nil
}
func (s *smtpSender) SendOnce(ctx context.Context, _ Envelope, i Intent) error {
	if err := s.validateIntents([]Intent{i}); err != nil {
		return err
	}
	if ctx.Err() != nil {
		return ErrSMTPOutcome
	}
	// A single exact From/To pair, constant non-user-controlled subject and quoted
	// printable MIME. Retained text cannot add headers, recipients or attachments.
	var body bytes.Buffer
	body.WriteString("From: " + s.config.From + "\r\nTo: " + i.Target + "\r\nSubject: HexThings alert\r\nDate: " + time.Now().UTC().Format(time.RFC1123Z) + "\r\nMIME-Version: 1.0\r\nContent-Type: text/plain; charset=utf-8\r\nContent-Transfer-Encoding: quoted-printable\r\n\r\n")
	qp := quotedprintable.NewWriter(&body)
	if _, err := qp.Write([]byte(i.Message)); err != nil {
		return ErrSMTPInput
	}
	if err := qp.Close(); err != nil {
		return ErrSMTPInput
	}
	c, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	conn, err := (&net.Dialer{Timeout: 3 * time.Second}).DialContext(c, "tcp", net.JoinHostPort(s.config.Host, strconv.Itoa(s.config.Port)))
	if err != nil {
		return ErrSMTPOutcome
	}
	defer conn.Close()
	stop := context.AfterFunc(c, func() { conn.Close() })
	defer stop()
	deadline, _ := c.Deadline()
	if conn.SetDeadline(deadline) != nil {
		return ErrSMTPOutcome
	}
	wire := &smtpWireBudget{Conn: conn, left: 65536}
	var secured net.Conn = wire
	if s.config.Mode == "implicit" {
		tc := tls.Client(wire, s.tls.Clone())
		if tc.HandshakeContext(c) != nil {
			return ErrSMTPOutcome
		}
		secured = tc
	}
	client, err := smtp.NewClient(secured, s.config.Host)
	if err != nil {
		return ErrSMTPOutcome
	}
	defer client.Close()
	if err = client.Hello("localhost"); err != nil {
		return ErrSMTPOutcome
	}
	if s.config.Mode == "starttls" {
		if ok, _ := client.Extension("STARTTLS"); !ok {
			return ErrSMTPOutcome
		}
		if err = client.StartTLS(s.tls.Clone()); err != nil {
			return ErrSMTPOutcome
		}
	}
	state, ok := client.TLSConnectionState()
	if !ok || !state.HandshakeComplete || len(state.VerifiedChains) == 0 {
		return ErrSMTPOutcome
	}
	if s.config.Username != "" {
		authOK, mechanisms := client.Extension("AUTH")
		plain := false
		for _, m := range strings.Fields(mechanisms) {
			if m == "PLAIN" {
				plain = true
			}
		}
		if !authOK || !plain {
			return ErrSMTPOutcome
		}
		if err = client.Auth(smtp.PlainAuth("", s.config.Username, s.config.Password, s.config.Host)); err != nil {
			return ErrSMTPOutcome
		}
	}
	if err = client.Mail(s.config.From); err != nil {
		return ErrSMTPOutcome
	}
	if err = client.Rcpt(i.Target); err != nil {
		return ErrSMTPOutcome
	}
	writer, err := client.Data()
	if err != nil {
		return ErrSMTPOutcome
	}
	if _, err = writer.Write(body.Bytes()); err != nil {
		return ErrSMTPOutcome
	}
	// DATA close waits for the final relay acknowledgement. A lost/error response
	// remains unknown even if the relay accepted the body. Never resend or retry.
	if err = writer.Close(); err != nil || c.Err() != nil {
		return ErrSMTPOutcome
	}
	// Relay acceptance is established. Close the connection without a second SMTP
	// exchange whose failure could incorrectly erase a known DATA acknowledgement.
	// This is NOT recipient delivery, a durable per-effect receipt or SMTP queue.
	return nil
}
