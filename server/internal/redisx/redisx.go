// Package redisx is a tiny Redis client over raw RESP (one short connection
// per command), enough for the optional shared state this product uses:
// OIDC login state and the shared response cache. No external dependency.
package redisx

import (
	"bufio"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"time"
)

// Client talks to one Redis server. The zero value is not usable; use New.
type Client struct {
	addr      string
	password  string
	username  string
	database  int
	tlsConfig *tls.Config
}

func New(addr, password string) *Client { return &Client{addr: addr, password: password} }

// Cmd runs one command and returns its reply as a string. A nil bulk reply is "".
func (r *Client) Cmd(args ...string) (string, error) {
	return r.CmdContext(context.Background(), args...)
}

// CmdContext bounds dialing and I/O by the caller's cancellation and deadline.
func (r *Client) CmdContext(ctx context.Context, args ...string) (string, error) {
	conn, err := (&net.Dialer{Timeout: 5 * time.Second}).DialContext(ctx, "tcp", r.addr)
	if err != nil {
		return "", err
	}
	defer conn.Close()
	deadline := time.Now().Add(5 * time.Second)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	conn.SetDeadline(deadline)
	rawConn := conn
	stop := context.AfterFunc(ctx, func() { rawConn.Close() })
	defer stop()
	if r.tlsConfig != nil {
		secure := tls.Client(conn, r.tlsConfig)
		if err := secure.HandshakeContext(ctx); err != nil {
			return "", errors.New("Redis TLS handshake failed")
		}
		conn = secure
	}
	w := bufio.NewWriter(conn)
	rd := bufio.NewReader(conn)
	write := func(args ...string) error {
		fmt.Fprintf(w, "*%d\r\n", len(args))
		for _, a := range args {
			fmt.Fprintf(w, "$%d\r\n%s\r\n", len(a), a)
		}
		return w.Flush()
	}
	if r.password != "" {
		auth := []string{"AUTH", r.password}
		if r.username != "" {
			auth = []string{"AUTH", r.username, r.password}
		}
		if err := write(auth...); err != nil {
			return "", err
		}
		if reply, err := readReply(rd); err != nil || reply != "OK" {
			return "", errors.New("Redis authentication failed")
		}
	}
	if r.database != 0 {
		if err := write("SELECT", strconv.Itoa(r.database)); err != nil {
			return "", err
		}
		if reply, err := readReply(rd); err != nil || reply != "OK" {
			return "", errors.New("Redis database selection failed")
		}
	}
	if err := write(args...); err != nil {
		return "", err
	}
	return readReply(rd)
}

func readReply(rd *bufio.Reader) (string, error) {
	// Bound both line and bulk responses before allocating memory. The product
	// stores response bodies up to 1 MiB; 8 MiB leaves room for metadata.
	line, err := rd.ReadSlice('\n')
	if err != nil {
		return "", err
	}
	if len(line) < 3 || line[len(line)-2] != '\r' {
		return "", errors.New("invalid Redis reply")
	}
	body := string(line[1 : len(line)-2])
	switch line[0] {
	case '+':
		return body, nil
	case '-':
		return "", errors.New("Redis command rejected") // never echo server-controlled text/credentials
	case ':':
		if _, err := strconv.ParseInt(body, 10, 64); err != nil {
			return "", errors.New("invalid Redis integer")
		}
		return body, nil
	case '$':
		n, err := strconv.Atoi(body)
		if err != nil || n < -1 || n > 8<<20 {
			return "", errors.New("invalid Redis bulk length")
		}
		if n == -1 {
			return "", nil
		}
		buf := make([]byte, n+2)
		if _, err := io.ReadFull(rd, buf); err != nil {
			return "", err
		}
		if buf[n] != '\r' || buf[n+1] != '\n' {
			return "", errors.New("invalid Redis bulk terminator")
		}
		return string(buf[:n]), nil
	}
	return "", errors.New("unexpected Redis reply type")
}

// Get returns the value and whether the key exists.
func (r *Client) Get(key string) (string, bool, error) {
	v, err := r.Cmd("GET", key)
	if err != nil {
		return "", false, err
	}
	return v, v != "", nil
}

// Set stores a value that expires after ttl (at least one second).
func (r *Client) Set(key, val string, ttl time.Duration) error {
	secs := int(ttl.Seconds())
	if secs < 1 {
		secs = 1
	}
	_, err := r.Cmd("SET", key, val, "EX", fmt.Sprint(secs))
	return err
}

// Incr atomically adds one and returns the new value.
func (r *Client) Incr(key string) (int64, error) {
	v, err := r.Cmd("INCR", key)
	if err != nil {
		return 0, err
	}
	var n int64
	_, err = fmt.Sscanf(v, "%d", &n)
	return n, err
}

// GetInt returns an integer key, 0 when absent.
func (r *Client) GetInt(key string) (int64, error) {
	v, ok, err := r.Get(key)
	if err != nil || !ok {
		return 0, err
	}
	var n int64
	_, err = fmt.Sscanf(v, "%d", &n)
	return n, err
}
