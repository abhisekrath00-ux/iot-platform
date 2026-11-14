package main

import (
	"context"
	"fmt"
	"log"
	"net"
	"net/http"
	"time"

	"github.com/abhisekrath00-ux/iot-platform/edge/internal/explorer"
)

var explorerDrivers = &explorer.Registry{}

// This is a separate, opt-in loopback listener, never the configurable LAN
// status mux. No connection, serial scan or Modbus transaction starts here.
func startExplorer(ctx context.Context, address string) error {
	if address == "" {
		return nil
	}
	host, _, err := net.SplitHostPort(address)
	ip := net.ParseIP(host)
	if err != nil || ip == nil || !ip.IsLoopback() {
		return fmt.Errorf("explorer listener must use a literal loopback IP")
	}
	ln, err := net.Listen("tcp", address)
	if err != nil {
		return err
	}
	h, err := explorer.New(ln.Addr().String(), explorerDrivers)
	if err != nil {
		ln.Close()
		return err
	}
	srv := &http.Server{Handler: h, ReadHeaderTimeout: 3 * time.Second, ReadTimeout: 5 * time.Second, WriteTimeout: 10 * time.Second, IdleTimeout: 30 * time.Second, MaxHeaderBytes: 8192}
	go func() {
		<-ctx.Done()
		c, cancel := context.WithTimeout(context.Background(), 7*time.Second)
		defer cancel()
		if srv.Shutdown(c) != nil {
			srv.Close()
		}
	}()
	go func() {
		if err := srv.Serve(ln); err != nil && err != http.ErrServerClosed {
			log.Printf("explorer stopped: %v", err)
		}
	}()
	log.Printf("read-only Modbus explorer: http://%s (owned connections only; no device read until Start)", ln.Addr())
	return nil
}
