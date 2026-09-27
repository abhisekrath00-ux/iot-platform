// Package oidcstate stores OIDC login state. In-memory for a single replica;
// Redis when REDIS_URL is set so multi-replica deployments share state and a
// callback can land on any node. The Redis client is a minimal RESP
// implementation - no external module - so the air-gapped bundle stays clean.
package oidcstate

import (
	"bufio"
	"errors"
	"fmt"
	"net"
	"strings"
	"sync"
	"time"
)

type Store interface {
	// Put records state -> nonce with a TTL.
	Put(state, nonce string, ttl time.Duration) error
	// Take returns and deletes the nonce for a state (single use).
	Take(state string) (string, bool)
}

// --- in-memory ---

type Memory struct {
	mu sync.Mutex
	m  map[string]memEntry
}

type memEntry struct {
	nonce   string
	expires time.Time
}

func NewMemory() *Memory { return &Memory{m: map[string]memEntry{}} }

func (s *Memory) Put(state, nonce string, ttl time.Duration) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for k, v := range s.m { // sweep expired
		if time.Now().After(v.expires) {
			delete(s.m, k)
		}
	}
	s.m[state] = memEntry{nonce, time.Now().Add(ttl)}
	return nil
}

func (s *Memory) Take(state string) (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.m[state]
	if ok {
		delete(s.m, state)
	}
	if !ok || time.Now().After(e.expires) {
		return "", false
	}
	return e.nonce, true
}

// --- Redis over raw RESP ---

type Redis struct {
	addr     string
	password string
}

func NewRedis(addr, password string) *Redis { return &Redis{addr: addr, password: password} }

func (r *Redis) cmd(args ...string) (string, error) {
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

func (r *Redis) Put(state, nonce string, ttl time.Duration) error {
	_, err := r.cmd("SET", "oidc:"+state, nonce, "EX", fmt.Sprint(int(ttl.Seconds())), "NX")
	return err
}

func (r *Redis) Take(state string) (string, bool) {
	// GETDEL: single use enforced atomically by the server (Redis >= 6.2).
	v, err := r.cmd("GETDEL", "oidc:"+state)
	if err != nil || v == "" {
		return "", false
	}
	return v, true
}
