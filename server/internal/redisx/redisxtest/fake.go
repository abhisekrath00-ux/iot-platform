// Package redisxtest is an in-process fake of the Redis commands this product
// uses (AUTH, GET, SET with EX/NX, GETDEL, INCR), for tests only.
package redisxtest

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"
)

type Server struct {
	Addr string
	ln   net.Listener
	mu   sync.Mutex
	m    map[string]string
	exp  map[string]time.Time
	Cmds int
	Down bool // when true, connections are refused (simulates an outage)
}

func Start() *Server {
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	s := &Server{Addr: ln.Addr().String(), ln: ln, m: map[string]string{}, exp: map[string]time.Time{}}
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go s.serve(c)
		}
	}()
	return s
}

func (s *Server) Close() { s.ln.Close() }

func (s *Server) serve(c net.Conn) {
	defer c.Close()
	rd := bufio.NewReader(c)
	for {
		args, err := readCmd(rd)
		if err != nil {
			return
		}
		s.mu.Lock()
		if s.Down {
			s.mu.Unlock()
			return
		}
		s.Cmds++
		reply := s.exec(args)
		s.mu.Unlock()
		io.WriteString(c, reply)
	}
}

func readCmd(rd *bufio.Reader) ([]string, error) {
	line, err := rd.ReadString('\n')
	if err != nil {
		return nil, err
	}
	n, _ := strconv.Atoi(strings.TrimSpace(line[1:]))
	args := make([]string, 0, n)
	for i := 0; i < n; i++ {
		l, err := rd.ReadString('\n')
		if err != nil {
			return nil, err
		}
		size, _ := strconv.Atoi(strings.TrimSpace(l[1:]))
		buf := make([]byte, size+2)
		if _, err := io.ReadFull(rd, buf); err != nil {
			return nil, err
		}
		args = append(args, string(buf[:size]))
	}
	return args, nil
}

func (s *Server) live(k string) (string, bool) {
	if e, ok := s.exp[k]; ok && time.Now().After(e) {
		delete(s.m, k)
		delete(s.exp, k)
	}
	v, ok := s.m[k]
	return v, ok
}

func bulk(v string, ok bool) string {
	if !ok {
		return "$-1\r\n"
	}
	return fmt.Sprintf("$%d\r\n%s\r\n", len(v), v)
}

func (s *Server) exec(a []string) string {
	switch strings.ToUpper(a[0]) {
	case "AUTH":
		return "+OK\r\n"
	case "GET":
		v, ok := s.live(a[1])
		return bulk(v, ok)
	case "GETDEL":
		v, ok := s.live(a[1])
		delete(s.m, a[1])
		return bulk(v, ok)
	case "INCR":
		v, _ := s.live(a[1])
		n, _ := strconv.ParseInt(v, 10, 64)
		n++
		s.m[a[1]] = strconv.FormatInt(n, 10)
		return fmt.Sprintf(":%d\r\n", n)
	case "SET":
		nx := false
		var ttl time.Duration
		for i := 3; i < len(a); i++ {
			switch strings.ToUpper(a[i]) {
			case "NX":
				nx = true
			case "EX":
				sec, _ := strconv.Atoi(a[i+1])
				ttl = time.Duration(sec) * time.Second
				i++
			}
		}
		if _, ok := s.live(a[1]); ok && nx {
			return "$-1\r\n"
		}
		s.m[a[1]] = a[2]
		delete(s.exp, a[1])
		if ttl > 0 {
			s.exp[a[1]] = time.Now().Add(ttl)
		}
		return "+OK\r\n"
	}
	return "-ERR unknown command\r\n"
}
