package notify

import (
	"bufio"
	"net"
	"strings"
	"testing"
	"time"
)

// captureSMTP runs a throwaway SMTP sink on loopback and returns the raw DATA payload.
func captureSMTP(t *testing.T, send func(addr string) error) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	got := make(chan string, 1)
	go func() {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		defer c.Close()
		r := bufio.NewReader(c)
		w := bufio.NewWriter(c)
		w.WriteString("220 sink\r\n")
		w.Flush()
		var body strings.Builder
		data := false
		for {
			line, err := r.ReadString('\n')
			if err != nil {
				break
			}
			if data {
				if line == "..\r\n" {
					body.WriteString(".\r\n")
					continue
				}
				if line == ".\r\n" {
					w.WriteString("250 queued\r\n")
					w.Flush()
					data = false
					continue
				}
				body.WriteString(line)
				continue
			}
			switch {
			case strings.HasPrefix(line, "DATA"):
				w.WriteString("354 go\r\n")
				data = true
			case strings.HasPrefix(line, "QUIT"):
				w.WriteString("221 bye\r\n")
			default:
				w.WriteString("250 ok\r\n")
			}
			w.Flush()
		}
		got <- body.String()
	}()
	if err := send(ln.Addr().String()); err != nil {
		t.Fatalf("send: %v", err)
	}
	select {
	case b := <-got:
		return b
	case <-time.After(5 * time.Second):
		t.Fatal("sink timed out")
		return ""
	}
}

func TestEmailHTMLMultipart(t *testing.T) {
	n := &Notifier{from: "reports@x.local"}
	raw := captureSMTP(t, func(addr string) error {
		host, port, _ := net.SplitHostPort(addr)
		n.smtpHost, n.smtpPort = host, port
		return n.EmailHTML(t.Context(), []string{"ops@x.local"}, "Report: Temp", "see the HTML part", "<html><body><h1>Temp</h1></body></html>")
	})
	for _, want := range []string{
		"From: reports@x.local", "To: ops@x.local", "Subject: Report: Temp",
		"Content-Type: multipart/alternative; boundary=\"hx-boundary-1\"",
		"Content-Type: text/plain; charset=utf-8\r\n\r\nsee the HTML part",
		"Content-Type: text/html; charset=utf-8\r\n\r\n<html><body><h1>Temp</h1></body></html>",
		"--hx-boundary-1--",
	} {
		if !strings.Contains(raw, want) {
			t.Fatalf("missing %q in:\n%s", want, raw)
		}
	}
	// part order: plain before html, html before the closing boundary
	if strings.Index(raw, "text/plain") > strings.Index(raw, "text/html") {
		t.Fatal("plain part must precede the html part")
	}
}

func TestEmailHTMLGuards(t *testing.T) {
	n := &Notifier{smtpHost: "127.0.0.1", smtpPort: "1", from: "reports@x.local"}
	if err := n.EmailHTML(t.Context(), []string{"a@b\r\nBCC: x@y"}, "s", "p", "h"); err == nil {
		t.Fatal("header injection must be refused")
	}
	if err := n.EmailHTML(t.Context(), nil, "s", "p", "h"); err == nil {
		t.Fatal("empty recipients must be refused")
	}
	n2 := &Notifier{from: "reports@x.local"}
	if err := n2.EmailHTML(t.Context(), []string{"a@b"}, "s", "p", "h"); err == nil {
		t.Fatal("unconfigured smtp must be refused")
	}
}
