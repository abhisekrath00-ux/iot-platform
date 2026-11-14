package main

import (
	"context"
	"testing"
	"time"

	"github.com/abhisekrath00-ux/iot-platform/edge/internal/config"
	"github.com/abhisekrath00-ux/iot-platform/edge/internal/explorer"
	"github.com/abhisekrath00-ux/iot-platform/edge/internal/localui"
)

func TestExplorerSupervisorBorrowsAndExpiresOwnedConnection(t *testing.T) {
	old := explorerDrivers
	explorerDrivers = &explorer.Registry{}
	defer func() { explorerDrivers = old }()
	cfg := &config.Config{Devices: []config.Device{{ID: "synthetic-no-traffic", Profile: "modbus-tcp", Host: "fixture.invalid", NetPort: 1502, Address: 7, Interval: time.Hour}}}
	// TCP constructor is lazy. No probe/poll/read is invoked in this test.
	sup := startSupervisor(cfg, nil, "", localui.NewTracker())
	stopped := false
	defer func() {
		if !stopped {
			sup.stop()
		}
	}()
	var cs []explorer.Connection
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		cs = explorerDrivers.Connections()
		if len(cs) == 1 {
			break
		}
		time.Sleep(time.Millisecond)
	}
	if len(cs) != 1 || cs[0].Name != "synthetic-no-traffic" || cs[0].Endpoint != "fixture.invalid:1502" || cs[0].Unit != 7 {
		t.Fatalf("missing owned driver snapshot: %+v", cs)
	}
	sup.stop()
	stopped = true
	if len(explorerDrivers.Connections()) != 0 {
		t.Fatal("stopped supervisor retained driver")
	}
	if _, err := explorerDrivers.Read(context.Background(), explorer.Range{Connection: cs[0].ID, Function: 3, Offset: 0, Count: 1}); err == nil {
		t.Fatal("old lifetime id survived shutdown")
	}
}
func TestExplorerListenerIsOffUnlessExplicitAndRefusesLAN(t *testing.T) {
	if err := startExplorer(context.Background(), ""); err != nil {
		t.Fatal(err)
	}
	for _, addr := range []string{"0.0.0.0:0", "192.168.1.10:0", "localhost:0", ":0", "[::]:0"} {
		if err := startExplorer(context.Background(), addr); err == nil {
			t.Fatal("nonliteral/nonloopback allowed", addr)
		}
	}
}
