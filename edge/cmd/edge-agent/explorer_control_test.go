package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"regexp"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/abhisekrath00-ux/iot-platform/edge/internal/explorer"
)

type controlBorrowed struct{ calls atomic.Int32 }

func (r *controlBorrowed) ReadRange(context.Context, int, int, int) ([]uint16, error) {
	r.calls.Add(1)
	return []uint16{1}, nil
}
func TestExplorerDesktopControlActualListenerNoServiceOrDeviceRead(t *testing.T) {
	old := explorerDrivers
	r := &explorer.Registry{}
	explorerDrivers = r
	ctx, cancel := context.WithCancel(context.Background())
	defer func() { cancel(); explorerDrivers = old }()
	borrowed := &controlBorrowed{}
	r.Add(explorer.Connection{Name: "synthetic existing device", Transport: "rtu", Endpoint: "not-a-real-port", Unit: 7}, borrowed)
	dir := t.TempDir()
	startExplorerControl(ctx, dir, "")
	receipt, err := explorer.RequestControl(context.Background(), dir, "open")
	if err != nil || receipt.URL == "" || receipt.ExpiresAt.IsZero() {
		t.Fatal(receipt, err)
	}
	client := &http.Client{Timeout: time.Second}
	res, err := client.Get(receipt.URL)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if !strings.Contains(string(b), "Modbus explorer") {
		t.Fatal("not the actual explorer page")
	}
	token := regexp.MustCompile(`const token='([^']+)'`).FindSubmatch(b)
	if len(token) != 2 {
		t.Fatal("no page session")
	}
	body := `{"transport":"tcp","host":"127.0.0.1","net_port":1,"unit":7}`
	req, _ := http.NewRequest("POST", receipt.URL+"/api/connect", strings.NewReader(body))
	req.Header.Set("X-Explorer-Session", string(token[1]))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", receipt.URL)
	res, err = client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	var temporary explorer.Connection
	json.NewDecoder(res.Body).Decode(&temporary)
	res.Body.Close()
	if res.StatusCode != 200 || !temporary.Temporary || len(r.Connections()) != 2 || borrowed.calls.Load() != 0 {
		t.Fatal("open read a device or failed temporary lifecycle", res.StatusCode, temporary)
	}
	if _, err := explorer.RequestControl(context.Background(), dir, "close"); err != nil {
		t.Fatal(err)
	}
	if res, err := client.Get(receipt.URL); err == nil {
		res.Body.Close()
		t.Fatal("listener survived explicit close")
	}
	if len(r.Connections()) != 1 || borrowed.calls.Load() != 0 {
		t.Fatal("close changed borrowed telemetry")
	}
	if _, err := r.Read(context.Background(), explorer.Range{Connection: temporary.ID, Function: 3, Count: 1}); err == nil {
		t.Fatal("closed temporary lifetime survived")
	}
	next, err := explorer.RequestControl(context.Background(), dir, "open")
	if err != nil {
		t.Fatal(err)
	}
	res, err = client.Get(next.URL)
	if err != nil {
		t.Fatal(err)
	}
	b, _ = io.ReadAll(res.Body)
	res.Body.Close()
	if strings.Contains(string(b), "const token='"+string(token[1])+"'") {
		t.Fatal("reopen retained old session capability")
	}
	cancel()
	deadline := time.Now().Add(2 * time.Second)
	closed := false
	for time.Now().Before(deadline) {
		res, err = client.Get(next.URL)
		if err != nil {
			closed = true
			break
		}
		res.Body.Close()
		time.Sleep(10 * time.Millisecond)
	}
	if !closed {
		t.Fatal("listener survived agent shutdown")
	}
}

func TestExplorerOwnerDeadlineAndOldTimerCannotCloseNewWindow(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	r := &explorer.Registry{}
	r.Add(explorer.Connection{Name: "borrowed"}, &controlBorrowed{})
	o := &explorerOwner{ctx: ctx, registry: r}
	defer func() { o.mu.Lock(); defer o.mu.Unlock(); o.closeLocked() }()
	url, first, err := o.apply("open")
	if err != nil {
		t.Fatal(err)
	}
	// Extend only by a second explicit Open. A stale timer must not close it.
	_, second, err := o.apply("open")
	if err != nil || !second.After(first) {
		t.Fatal(err)
	}
	o.expire(first)
	client := &http.Client{Timeout: time.Second}
	res, err := client.Get(url)
	if err != nil {
		t.Fatal("old deadline closed extended window", err)
	}
	res.Body.Close()
	o.expire(second)
	if res, err := client.Get(url); err == nil {
		res.Body.Close()
		t.Fatal("deadline left listener open")
	}
	if len(r.Connections()) != 1 {
		t.Fatal("deadline removed borrowed driver")
	}
}
