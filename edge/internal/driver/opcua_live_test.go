package driver

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/abhisekrath00-ux/iot-platform/edge/internal/config"
	"github.com/gopcua/opcua/server"
	"github.com/gopcua/opcua/ua"
)

// Runs the real driver against an in-process OPC UA server (no security).
func TestOPCUALivePoll(t *testing.T) {
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	port := ln.Addr().(*net.TCPAddr).Port
	ln.Close()

	srv := server.New(
		server.EndPoint("127.0.0.1", port),
		server.EnableSecurity("None", ua.MessageSecurityModeNone),
		server.EnableAuthMode(ua.UserTokenTypeAnonymous),
	)
	ns := server.NewNodeNameSpace(srv, "plant")
	ns.AddNewVariableStringNode("Boiler.Temp", float64(81.5))
	ns.AddNewVariableStringNode("Pump.Running", true)
	srv.AddNamespace(ns)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := srv.Start(ctx); err != nil {
		t.Skipf("cannot start in-process OPC UA server: %v", err)
	}
	defer srv.Close()
	time.Sleep(300 * time.Millisecond)

	d, err := NewWithOpener(nil, config.Device{
		ID: "scada", Profile: "opcua", Host: "127.0.0.1", NetPort: port,
		Points: []config.Point{
			{ID: "temp", NodeID: "ns=1;s=Boiler.Temp", Unit: "C", Min: 0, Max: 400},
			{ID: "pump", NodeID: "ns=1;s=Pump.Running", Min: 0, Max: 1},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	pctx, pc := context.WithTimeout(ctx, 10*time.Second)
	defer pc()
	r, err := d.Poll(pctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(r) != 2 || r[0].Value != 81.5 || r[1].Value != 1 {
		t.Fatalf("unexpected readings: %+v", r)
	}
}
