package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/abhisekrath00-ux/iot-platform/server/internal/redisx"
)

func TestReadinessDependencies(t *testing.T) {
	for _, tc := range []struct {
		name      string
		db, state error
		code      int
	}{
		{"database only", nil, nil, 200},
		{"database unavailable", errors.New("postgres://secret@private-db"), nil, 503},
		{"shared state unavailable", nil, errors.New("redis://password@private-state"), 503},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			checks := []func(context.Context) error{func(ctx context.Context) error {
				if d, ok := ctx.Deadline(); !ok || time.Until(d) > 2*time.Second {
					t.Error("missing bounded context")
				}
				return tc.db
			}}
			if tc.name != "database only" {
				checks = append(checks, func(context.Context) error { calls++; return tc.state })
			}
			w := httptest.NewRecorder()
			readinessHandler(checks...)(w, httptest.NewRequest("GET", "/readyz", nil))
			if w.Code != tc.code {
				t.Fatalf("status %d: %s", w.Code, w.Body.String())
			}
			if w.Header().Get("Cache-Control") != "no-store" {
				t.Error("readiness must not be cached")
			}
			if strings.Contains(w.Body.String(), "private") || strings.Contains(w.Body.String(), "secret") || strings.Contains(w.Body.String(), "password") {
				t.Fatal("dependency secret leaked")
			}
			if tc.db != nil && calls != 0 {
				t.Error("failed database must short circuit")
			}
		})
	}
}

func TestReadinessStateStoreTimeout(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	done := make(chan struct{})
	go func() {
		c, err := listener.Accept()
		if err != nil {
			close(done)
			return
		}
		defer c.Close()
		b := make([]byte, 256)
		c.Read(b) // accepts the port and PING, never answers
		c.Read(b) // client cancellation must close the connection
		close(done)
	}()
	cli := redisx.New(listener.Addr().String(), "")
	check := func(ctx context.Context) error { _, err := cli.CmdContext(ctx, "PING"); return err }
	w := httptest.NewRecorder()
	start := time.Now()
	readinessHandler(check)(w, httptest.NewRequest("GET", "/readyz", nil))
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status %d", w.Code)
	}
	if elapsed := time.Since(start); elapsed < time.Second || elapsed > 3*time.Second {
		t.Fatalf("timeout %v", elapsed)
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("state-store connection not closed")
	}
}

func TestReadinessStateStoreAuthentication(t *testing.T) {
	for _, tc := range []struct {
		name, auth, ping string
		code             int
	}{
		{"authenticated ready", "+OK\r\n", "+PONG\r\n", 200},
		{"wrong password", "-WRONGPASS private-password\r\n", "", 503},
		{"unexpected reply", "+OK\r\n", "+not-pong\r\n", 503},
	} {
		t.Run(tc.name, func(t *testing.T) {
			l, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			defer l.Close()
			done := make(chan error, 1)
			go func() {
				c, err := l.Accept()
				if err != nil {
					done <- err
					return
				}
				defer c.Close()
				c.SetDeadline(time.Now().Add(time.Second))
				rd := bufio.NewReader(c)
				expected := []string{"*2\r\n", "$4\r\n", "AUTH\r\n", "$16\r\n", "private-password\r\n"}
				for _, want := range expected {
					got, err := rd.ReadString('\n')
					if err != nil || got != want {
						done <- fmt.Errorf("AUTH shape mismatch")
						return
					}
				}
				fmt.Fprint(c, tc.auth)
				if tc.ping != "" {
					for _, want := range []string{"*1\r\n", "$4\r\n", "PING\r\n"} {
						got, err := rd.ReadString('\n')
						if err != nil || got != want {
							done <- fmt.Errorf("PING shape mismatch")
							return
						}
					}
					fmt.Fprint(c, tc.ping)
				}
				done <- nil
			}()
			w := httptest.NewRecorder()
			readinessHandler(stateStoreReadiness(redisx.New(l.Addr().String(), "private-password")))(w, httptest.NewRequest("GET", "/readyz", nil))
			if w.Code != tc.code {
				t.Fatalf("status %d body %q", w.Code, w.Body.String())
			}
			if strings.Contains(w.Body.String(), "password") {
				t.Fatal("AUTH error leaked")
			}
			if err := <-done; err != nil {
				t.Fatal(err)
			}
		})
	}
}
