package redisx

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"net"
	"net/url"
	"os"
	"strconv"
	"strings"
)

// TLSOptions supplies files for verified TLS. There is no skip-verification mode.
// Client certificates are optional and must be supplied as a pair.
type TLSOptions struct{ CAFile, CertFile, KeyFile string }

// FromURL configures one Redis/Valkey endpoint, not Sentinel or Cluster routing.
// Bare host:port remains supported for existing plaintext configurations.
// URLs accept redis:// or rediss://, optional ACL credentials and /database.
// Query parameters and fragments are refused rather than silently ignored.
func FromURL(raw string, opt TLSOptions) (*Client, error) {
	fail := func() (*Client, error) {
		return nil, errors.New("invalid REDIS_URL (use redis:// or rediss://[user:password@]host:port[/database]; no query or fragment)")
	}
	if raw == "" || strings.TrimSpace(raw) != raw {
		return fail()
	}
	if !strings.Contains(raw, "://") {
		if _, _, err := net.SplitHostPort(raw); err != nil {
			return fail()
		}
		raw = "redis://" + raw
	}
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "redis" && u.Scheme != "rediss") || u.Hostname() == "" || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.Opaque != "" {
		return fail()
	}
	port := u.Port()
	if port == "" {
		port = "6379"
	}
	p, err := strconv.Atoi(port)
	if err != nil || p < 1 || p > 65535 {
		return fail()
	}
	db := 0
	if u.Path != "" && u.Path != "/" {
		s := strings.TrimPrefix(u.Path, "/")
		if s == "" || strings.IndexFunc(s, func(c rune) bool { return c < '0' || c > '9' }) >= 0 {
			return fail()
		}
		db, err = strconv.Atoi(s)
		if err != nil || db > 2147483647 {
			return fail()
		}
	}
	c := New(net.JoinHostPort(u.Hostname(), port), "")
	c.database = db
	if u.User != nil {
		c.username = u.User.Username()
		c.password, _ = u.User.Password()
		if c.password == "" {
			return fail()
		}
	}
	if u.Scheme != "rediss" {
		if opt.CAFile != "" || opt.CertFile != "" || opt.KeyFile != "" {
			return nil, errors.New("Redis TLS files require rediss://")
		}
		return c, nil
	}
	c.tlsConfig = &tls.Config{MinVersion: tls.VersionTLS12, ServerName: u.Hostname()}
	if opt.CAFile != "" {
		b, err := os.ReadFile(opt.CAFile)
		if err != nil {
			return nil, errors.New("cannot read Redis TLS CA file")
		}
		roots, err := x509.SystemCertPool()
		if err != nil {
			roots = x509.NewCertPool()
		}
		if !roots.AppendCertsFromPEM(b) {
			return nil, errors.New("invalid Redis TLS CA file")
		}
		c.tlsConfig.RootCAs = roots
	}
	if (opt.CertFile == "") != (opt.KeyFile == "") {
		return nil, errors.New("Redis TLS client certificate and key must be supplied together")
	}
	if opt.CertFile != "" {
		cert, err := tls.LoadX509KeyPair(opt.CertFile, opt.KeyFile)
		if err != nil {
			return nil, errors.New("invalid Redis TLS client certificate/key pair")
		}
		c.tlsConfig.Certificates = []tls.Certificate{cert}
	}
	return c, nil
}
