package redisx

import (
	"bufio"
	"strings"
	"testing"
)

func TestURLConfiguration(t *testing.T) {
	for _, raw := range []string{"redis://localhost", "localhost:6379", "redis://:p%40ss@localhost:6380/2", "rediss://user:p%3Ass@[::1]:6379/0"} {
		c, err := FromURL(raw, TLSOptions{})
		if err != nil {
			t.Fatalf("valid URL: %v", err)
		}
		if strings.HasPrefix(raw, "rediss:") && (c.tlsConfig == nil || c.tlsConfig.InsecureSkipVerify) {
			t.Fatal("verified TLS required")
		}
	}
	c, _ := FromURL("redis://worker:p%40ss@localhost:6380/2", TLSOptions{})
	if c.username != "worker" || c.password != "p@ss" || c.database != 2 {
		t.Fatal("credentials/database decoding")
	}
	for _, raw := range []string{"", " localhost:6379", "http://localhost:6379", "redis://", "redis://user@localhost", "redis://:secret@localhost/abc", "redis://localhost/-1", "redis://localhost/2/3", "redis://localhost/2147483648", "redis://localhost:0", "redis://localhost:65536", "rediss://localhost?insecure=true", "redis://localhost#secret", "redis://localhost?"} {
		if _, err := FromURL(raw, TLSOptions{}); err == nil {
			t.Fatalf("accepted invalid URL %q", raw)
		} else if strings.Contains(err.Error(), "secret") {
			t.Fatal("URL leaked")
		}
	}
	for _, o := range []TLSOptions{{CAFile: "missing"}, {CertFile: "missing"}, {KeyFile: "missing"}} {
		if _, err := FromURL("redis://localhost", o); err == nil {
			t.Fatal("plaintext accepted TLS files")
		}
		if _, err := FromURL("rediss://localhost", o); err == nil {
			t.Fatal("invalid TLS files accepted")
		}
	}
}

func TestReplyBounds(t *testing.T) {
	for _, s := range []string{"$bad\r\n", "$-2\r\n", "$8388609\r\n", "$2\r\nabXX", ":nope\r\n", "+OK\n", "*2\r\n", strings.Repeat("x", 8192) + "\r\n"} {
		if _, err := readReply(bufio.NewReader(strings.NewReader(s))); err == nil {
			t.Fatal("accepted malformed reply")
		}
	}
	if _, err := readReply(bufio.NewReader(strings.NewReader("-ERR do not reveal secret-value\r\n"))); err == nil || strings.Contains(err.Error(), "secret-value") {
		t.Fatal("server error leaked")
	}
}
