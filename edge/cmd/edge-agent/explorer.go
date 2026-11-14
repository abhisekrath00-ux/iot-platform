package main

import (
	"context"
	"fmt"
	"log"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/abhisekrath00-ux/iot-platform/edge/internal/explorer"
)

var explorerDrivers = &explorer.Registry{}

type explorerListener struct {
	url  string
	stop func()
	done <-chan struct{}
}

// This is a separate, opt-in loopback listener, never the configurable LAN
// status mux. No connection, serial scan or Modbus transaction starts here.
func listenExplorer(parent context.Context, address string, registry *explorer.Registry) (*explorerListener, error) {
	host, _, err := net.SplitHostPort(address)
	ip := net.ParseIP(host)
	if err != nil || ip == nil || !ip.IsLoopback() {
		return nil, fmt.Errorf("explorer listener must use a literal loopback IP")
	}
	if err := parent.Err(); err != nil {
		return nil, err
	}
	ln, err := net.Listen("tcp", address)
	if err != nil {
		return nil, err
	}
	h, err := explorer.New(ln.Addr().String(), registry)
	if err != nil {
		ln.Close()
		return nil, err
	}
	ctx, cancel := context.WithCancel(parent)
	registryDone := registry.EnableTemporary(ctx)
	srv := &http.Server{Handler: h, ReadHeaderTimeout: 3 * time.Second, ReadTimeout: 5 * time.Second, WriteTimeout: 10 * time.Second, IdleTimeout: 30 * time.Second, MaxHeaderBytes: 8192}
	served, stopped := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(served)
		defer cancel()
		if err := srv.Serve(ln); err != nil && err != http.ErrServerClosed {
			log.Printf("explorer stopped: %v", err)
		}
	}()
	go func() {
		defer close(stopped)
		<-ctx.Done()
		c, end := context.WithTimeout(context.Background(), 7*time.Second)
		defer end()
		if srv.Shutdown(c) != nil {
			srv.Close()
		}
		<-served
		<-registryDone
	}()
	return &explorerListener{url: "http://" + ln.Addr().String(), done: stopped, stop: func() { cancel(); <-stopped }}, nil
}
func startExplorer(ctx context.Context, address string) error {
	if address == "" {
		return nil
	}
	s, err := listenExplorer(ctx, address, explorerDrivers)
	if err == nil {
		log.Printf("read-only Modbus explorer: %s (no device read until Start; temporary connections are not saved)", s.url)
	}
	return err
}

type explorerOwner struct {
	mu       sync.Mutex
	ctx      context.Context
	registry *explorer.Registry
	session  *explorerListener
	expires  time.Time
	timer    *time.Timer
}

func (o *explorerOwner) closeLocked() {
	if o.timer != nil {
		o.timer.Stop()
		o.timer = nil
	}
	if o.session != nil {
		o.session.stop()
		o.session = nil
	}
	o.expires = time.Time{}
}
func (o *explorerOwner) apply(action string) (string, time.Time, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if action == "close" {
		o.closeLocked()
		return "", time.Time{}, nil
	}
	if action != "open" {
		return "", time.Time{}, fmt.Errorf("unknown explorer action")
	}
	if err := o.ctx.Err(); err != nil {
		return "", time.Time{}, err
	}
	if o.session != nil {
		select {
		case <-o.session.done:
			o.session = nil
		default:
		}
	}
	if o.session == nil {
		s, err := listenExplorer(o.ctx, "127.0.0.1:0", o.registry)
		if err != nil {
			return "", time.Time{}, err
		}
		o.session = s
	}
	if o.timer != nil {
		o.timer.Stop()
	}
	o.expires = time.Now().Add(time.Hour)
	o.timer = time.AfterFunc(time.Hour, func() { o.expire(time.Now()) })
	return o.session.url, o.expires, nil
}
func (o *explorerOwner) expire(now time.Time) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if !now.Before(o.expires) {
		o.closeLocked()
	}
}
func startExplorerControl(ctx context.Context, dir, address string) {
	o := &explorerOwner{ctx: ctx, registry: explorerDrivers}
	if address != "" {
		s, err := listenExplorer(ctx, address, o.registry)
		if err != nil {
			log.Printf("explorer disabled: %v", err)
		} else {
			o.session = s
			log.Printf("read-only Modbus explorer: %s", s.url)
		}
	}
	go explorer.RunControl(ctx, dir, o.apply)
	go func() { <-ctx.Done(); o.mu.Lock(); defer o.mu.Unlock(); o.closeLocked() }()
}
