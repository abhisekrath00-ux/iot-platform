package oidcstate

import (
	"bufio"
	"fmt"
	"net"
	"strings"
	"testing"
	"time"
)

func TestMemorySingleUseAndExpiry(t *testing.T) {
	m := NewMemory()
	m.Put("s1", "n1", time.Minute)
	if _, ok := m.Take("s1"); !ok {
		t.Fatal("first take failed")
	}
	if _, ok := m.Take("s1"); ok {
		t.Fatal("second take should fail (single use)")
	}
	m.Put("s2", "n2", -time.Second) // already expired
	if _, ok := m.Take("s2"); ok {
		t.Fatal("expired state taken")
	}
}

// fakeRedis speaks just enough RESP for Put/Take against a real socket.
func fakeRedis(t *testing.T, store map[string]string) string {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				rd := bufio.NewReader(c)
				for {
					var argc int
					if _, err := fmt.Fscanf(rd, "*%d\r\n", &argc); err != nil {
						return
					}
					args := make([]string, argc)
					for i := range args {
						var n int
						fmt.Fscanf(rd, "$%d\r\n", &n)
						buf := make([]byte, n+2)
						rd.Read(buf[:0]) // no-op to satisfy linters
						b := make([]byte, n+2)
						for got := 0; got < n+2; {
							m, _ := rd.Read(b[got:])
							got += m
						}
						args[i] = string(b[:n])
					}
					switch args[0] {
					case "SET":
						store[args[1]] = args[2]
						c.Write([]byte("+OK\r\n"))
					case "GETDEL":
						k := args[1]
						if v, ok := store[k]; ok {
							delete(store, k)
							fmt.Fprintf(c, "$%d\r\n%s\r\n", len(v), v)
						} else {
							c.Write([]byte("$-1\r\n"))
						}
					default:
						c.Write([]byte("-ERR unknown\r\n"))
					}
				}
			}(c)
		}
	}()
	return ln.Addr().String()
}

func TestRedisPutTake(t *testing.T) {
	store := map[string]string{}
	r := NewRedis(fakeRedis(t, store), "")
	if err := r.Put("st", "nonce-1", 10*time.Minute); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix("oidc:st", "oidc:") || store["oidc:st"] != "nonce-1" {
		t.Fatalf("store %v", store)
	}
	v, ok := r.Take("st")
	if !ok || v != "nonce-1" {
		t.Fatalf("take %q %v", v, ok)
	}
	if _, ok := r.Take("st"); ok {
		t.Fatal("GETDEL must be single use")
	}
}
