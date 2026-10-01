package notify

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"syscall"
	"time"
)

// ValidateWebhookURL is the admin-time check for a webhook channel target.
func ValidateWebhookURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" || u.User != nil {
		return errors.New("webhook target must be an http(s) URL without credentials")
	}
	if len(raw) > 512 {
		return errors.New("webhook target too long")
	}
	return nil
}

// blockedIP reports addresses a webhook must never reach: loopback, link-local
// (including the 169.254.169.254 cloud metadata address), unspecified and
// multicast. Private RFC 1918 ranges stay allowed because air-gapped and
// on-prem receivers live there; only a tenant admin can configure the URL.
func blockedIP(ip net.IP) bool {
	return ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsUnspecified() || ip.IsMulticast()
}

// safeClient checks the resolved address at dial time, so a hostname cannot
// pass validation and later rebind to a blocked address. Redirects are refused.
func safeClient(allowLoopback bool) *http.Client {
	d := &net.Dialer{Timeout: 5 * time.Second, Control: func(_, address string, _ syscall.RawConn) error {
		host, _, err := net.SplitHostPort(address)
		if err != nil {
			return err
		}
		ip := net.ParseIP(host)
		if ip == nil || (blockedIP(ip) && !(allowLoopback && ip.IsLoopback())) {
			return fmt.Errorf("webhook: address %s is not allowed", host)
		}
		return nil
	}}
	return &http.Client{
		Timeout:       8 * time.Second,
		Transport:     &http.Transport{DialContext: d.DialContext, DisableKeepAlives: true},
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
}

// Webhook POSTs a JSON event to a tenant-configured URL. When
// WEBHOOK_SIGNING_SECRET is set, X-Hexmon-Signature carries sha256=<hex HMAC of
// the body> so the receiver can verify the sender.
func (n *Notifier) Webhook(ctx context.Context, target, event string, data map[string]any) error {
	if err := ValidateWebhookURL(target); err != nil {
		return err
	}
	body, _ := json.Marshal(map[string]any{"event": event, "sent_at": time.Now().UTC().Format(time.RFC3339), "data": data})
	req, err := http.NewRequestWithContext(ctx, "POST", target, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "hexmon-iot-webhook/1")
	if sec := os.Getenv("WEBHOOK_SIGNING_SECRET"); sec != "" {
		m := hmac.New(sha256.New, []byte(sec))
		m.Write(body)
		req.Header.Set("X-Hexmon-Signature", "sha256="+hex.EncodeToString(m.Sum(nil)))
	}
	resp, err := safeClient(n.allowLoopback).Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return fmt.Errorf("webhook: status %d", resp.StatusCode)
	}
	return nil
}
