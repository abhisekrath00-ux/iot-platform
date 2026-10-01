// Package redisx is a tiny Redis client over raw RESP (one short connection
// per command), enough for the optional shared state this product uses:
// OIDC login state and the shared response cache. No external dependency.
package redisx

import (
	"bufio"
	"errors"
	"fmt"
	"net"
	"strings"
	"time"
)

// Client talks to one Redis server. The zero value is not usable; use New.
type Client struct {
	addr     string
	password string
}

func New(addr, password string) *Client { return &Client{addr: addr, password: password} }

// Cmd runs one command and returns its reply as a string. A nil bulk reply is "".
func (r *Client) Cmd(args ...string) (string, error) {
	conn, err := net.DialTimeout("tcp", r.addr, 5*time.Second)
	if err != nil {
		return "", err
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(5 * time.Second))
	w := bufio.NewWriter(conn)
	write := func(args ...string) error {
		fmt.Fprintf(w, "*%d\r\n", len(args))
		for _, a := range args {
			fmt.Fprintf(w, "$%d\r\n%s\r\n", len(a), a)
		}
		return w.Flush()
	}
	if r.password != "" {
		if err := write("AUTH", r.password); err != nil {
			return "", err
		}
		if _, err := readReply(bufio.NewReader(conn)); err != nil {
			return "", fmt.Errorf("auth: %w", err)
		}
	}
	if err := write(args...); err != nil {
		return "", err
	}
	return readReply(bufio.NewReader(conn))
}

func readReply(rd *bufio.Reader) (string, error) {
	line, err := rd.ReadString('\n')
	if err != nil {
		return "", err
	}
	line = strings.TrimRight(line, "\r\n")
	if line == "" {
		return "", errors.New("empty reply")
	}
	switch line[0] {
	case '+':
		return line[1:], nil
	case '-':
		return "", errors.New(line[1:])
	case '$':
		var n int
		fmt.Sscanf(line[1:], "%d", &n)
		if n < 0 {
			return "", nil // nil bulk
		}
		buf := make([]byte, n+2)
		if _, err := readFull(rd, buf); err != nil {
			return "", err
		}
		return string(buf[:n]), nil
	case ':':
		return line[1:], nil
	}
	return "", fmt.Errorf("unexpected reply %q", line)
}

func readFull(rd *bufio.Reader, buf []byte) (int, error) {
	n := 0
	for n < len(buf) {
		m, err := rd.Read(buf[n:])
		n += m
		if err != nil {
			return n, err
		}
	}
	return n, nil
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
