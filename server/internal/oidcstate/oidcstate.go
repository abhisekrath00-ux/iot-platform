// Package oidcstate stores OIDC login state. In-memory for a single replica;
// Redis when REDIS_URL is set so multi-replica deployments share state and a
// callback can land on any node. The Redis client is a minimal RESP
// implementation - no external module - so the air-gapped bundle stays clean.
package oidcstate

import (
	"fmt"
	"github.com/abhisekrath00-ux/iot-platform/server/internal/redisx"
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

// --- Redis ---

type Redis struct{ c *redisx.Client }

func NewRedis(addr, password string) *Redis { return &Redis{c: redisx.New(addr, password)} }

func (r *Redis) Put(state, nonce string, ttl time.Duration) error {
	_, err := r.c.Cmd("SET", "oidc:"+state, nonce, "EX", fmt.Sprint(int(ttl.Seconds())), "NX")
	return err
}

func (r *Redis) Take(state string) (string, bool) {
	// GETDEL: single use enforced atomically by the server (Redis >= 6.2).
	v, err := r.c.Cmd("GETDEL", "oidc:"+state)
	if err != nil || v == "" {
		return "", false
	}
	return v, true
}
