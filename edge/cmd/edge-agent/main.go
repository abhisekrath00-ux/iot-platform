// edge-agent runs on the gateway SBC: polls configured serial devices,
// buffers validated readings in SQLite, publishes over MQTT, and executes
// only allowlisted, approved, unexpired commands.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"sync"
	"sync/atomic"
	"time"

	"github.com/abhisekrath00-ux/iot-platform/edge/internal/claim"
	"github.com/abhisekrath00-ux/iot-platform/edge/internal/cmdexec"
	"github.com/abhisekrath00-ux/iot-platform/edge/internal/config"
	"github.com/abhisekrath00-ux/iot-platform/edge/internal/discover"
	"github.com/abhisekrath00-ux/iot-platform/edge/internal/driver"
	"github.com/abhisekrath00-ux/iot-platform/edge/internal/fleetctl"
	"github.com/abhisekrath00-ux/iot-platform/edge/internal/localrules"
	"github.com/abhisekrath00-ux/iot-platform/edge/internal/localui"
	"github.com/abhisekrath00-ux/iot-platform/edge/internal/mqttc"
	"github.com/abhisekrath00-ux/iot-platform/edge/internal/paths"
	"github.com/abhisekrath00-ux/iot-platform/edge/internal/queue"
	mqtt "github.com/eclipse/paho.mqtt.golang"
	"github.com/google/uuid"
)

type envelope struct {
	EventID       string  `json:"event_id"`
	TenantID      string  `json:"tenant_id"`
	GatewayID     string  `json:"gateway_id"`
	DeviceID      string  `json:"device_id"`
	PointID       string  `json:"point_id"`
	ObservedAt    string  `json:"observed_at"`
	Value         float64 `json:"value"`
	Unit          string  `json:"unit"`
	Quality       string  `json:"quality"`
	SchemaVersion int     `json:"schema_version"`
}

type command struct {
	RequestID string          `json:"request_id"`
	Action    string          `json:"action"`
	Params    json.RawMessage `json:"parameters"`
	ExpiresAt time.Time       `json:"expires_at"`
}

var version = "dev"

func main() {
	cfgPath := flag.String("config", paths.Config(), "config file")
	claimAPI := flag.String("claim-api", "", "control-plane API base URL for enrollment (e.g. https://api.hexmon.example)")
	claimCode := flag.String("claim-code", "", "one-time enrollment claim code from the dashboard")
	claimSerial := flag.String("claim-serial", "", "gateway serial to claim")
	enrollStr := flag.String("enroll", "", "enroll string from the dashboard (server URL, code and serial in one value)")
	scanPort := flag.String("scan-port", "", "read-only Modbus RTU slave scan on this serial port (COM3 or /dev/ttyUSB0), prints responding addresses, then exits")
	scanBaud := flag.Int("scan-baud", 9600, "scan: baud rate")
	scanParity := flag.String("scan-parity", "none", "scan: none|even|odd")
	scanFrom := flag.Int("scan-from", 1, "scan: first slave address")
	scanTo := flag.Int("scan-to", 247, "scan: last slave address")
	scanFunc := flag.Int("scan-func", 4, "scan: Modbus function, 3 or 4 (reads only)")
	scanReg := flag.Int("scan-register", 0, "scan: a register the meter model implements")
	silenceOut := flag.String("silence", "", "silence this alarm output on the running agent for -for, then exit")
	testOut := flag.String("test-output", "", "sound this alarm output for a few seconds on the running agent, then exit")
	forDur := flag.Duration("for", 10*time.Minute, "duration for -silence (max 24h)")
	identityDir := flag.String("identity-dir", paths.Data(), "directory holding identity.json")
	discoverFlag := flag.Bool("discover", false, "look for Hexmon servers advertising on the local network (mDNS), print their addresses, then exit; nothing is enrolled")
	scanLAN := flag.String("scan-lan", "", "probe a private IPv4 range (e.g. 192.168.1.0/24, max /22) for open industrial ports (Modbus TCP, OPC UA, IEC 104, DNP3, MQTT), print results, then exit")
	whoIs := flag.String("scan-bacnet", "", "broadcast a BACnet Who-Is to this subnet broadcast address (e.g. 192.168.1.255, or 255.255.255.255) and list devices that answer, then exit")
	flag.Parse()

	if *whoIs != "" {
		ds, err := discover.FindBACnet(context.Background(), *whoIs, 3*time.Second)
		if err != nil {
			log.Fatalf("scan-bacnet: %v", err)
		}
		for _, d := range ds {
			fmt.Printf("%s  device,%d  vendor %d\n", d.Addr, d.Instance, d.Vendor)
		}
		fmt.Printf("%d BACnet device(s) answered. Use host=<address> in a bacnet profile.\n", len(ds))
		return
	}
	if *scanLAN != "" {
		hits, err := discover.ScanLAN(context.Background(), *scanLAN, 400*time.Millisecond)
		if err != nil {
			log.Fatalf("scan-lan: %v", err)
		}
		for _, h := range hits {
			fmt.Printf("%s:%d  %s\n", h.Addr, h.Port, h.Service)
		}
		fmt.Printf("%d open port(s). An open port means something is listening, not that it is a supported device.\n", len(hits))
		return
	}
	if *discoverFlag {
		ss, err := discover.FindServers(context.Background(), 3*time.Second)
		if err != nil {
			log.Fatalf("discover: %v", err)
		}
		if len(ss) == 0 {
			fmt.Println("no server answered. Multicast may be blocked on this network; type -claim-api by hand.")
			return
		}
		for _, s := range ss {
			fmt.Printf("%s  (host %s, answered by %s)\n", s.URL(), s.Host, s.From)
		}
		fmt.Println("Confirm the address with your administrator, then use: -claim-api <url> -claim-serial <serial> -claim-code <code>")
		return
	}

	if *silenceOut != "" || *testOut != "" {
		action, out, d := "silence", *silenceOut, *forDur
		if *testOut != "" {
			action, out, d = "test", *testOut, 5*time.Second
		}
		if err := localrules.WriteControl(controlPath(*identityDir), action, out, d); err != nil {
			log.Fatalf("%s: %v", action, err)
		}
		fmt.Printf("%s requested for %s; the running agent applies it within a second\n", action, out)
		return
	}
	if *scanPort != "" {
		found, err := driver.ScanModbusRTU(context.Background(), nil, driver.ScanRequest{
			Port: *scanPort, Baud: *scanBaud, DataBits: 8, StopBits: 1, Parity: *scanParity,
			From: *scanFrom, To: *scanTo, Func: *scanFunc, Register: *scanReg})
		if err != nil {
			log.Fatalf("scan: %v", err)
		}
		for _, f := range found {
			fmt.Printf("slave %d answered register %d with %d\n", f.Address, *scanReg, f.Value)
		}
		fmt.Printf("%d device(s) found\n", len(found))
		return
	}
	if *enrollStr != "" {
		e, err := claim.ParseEnroll(*enrollStr)
		if err != nil {
			log.Fatalf("enroll: %v", err)
		}
		*claimAPI, *claimCode, *claimSerial = e.API, e.Code, e.Serial
	}
	if *claimCode != "" {
		// Enrollment mode: redeem the claim code, persist identity, exit.
		if *claimAPI == "" || *claimSerial == "" {
			log.Fatal("-claim-api and -claim-serial are required with -claim-code")
		}
		id, err := claim.Redeem(context.Background(), *claimAPI, *claimCode, *claimSerial)
		if err != nil {
			log.Fatalf("claim: %v", err)
		}
		if err := claim.SaveIdentity(*identityDir, id); err != nil {
			log.Fatalf("claim: %v", err)
		}
		log.Printf("claimed: gateway %s enrolled; identity saved to %s/identity.json", id.GatewayID, *identityDir)
		return
	}

	cfg, err := config.Load(*cfgPath)
	if err != nil {
		log.Fatalf("config: %v", err)
	}
	cfg.DefaultIdentityFiles(*identityDir)
	q, err := queue.Open(cfg.QueuePath)
	if err != nil {
		log.Fatalf("queue: %v", err)
	}
	defer q.Close()

	ctx, stop, svcDone := rootContext()
	defer svcDone()
	defer stop()

	// Commands pass cmdexec's gate (allowlist, expiry, TTL, replay) and are then
	// executed by the configured actuator. Only "simulate" exists today; with no
	// command_mode every command is rejected and acked as rejected.
	gate := cmdexec.NewGate(cfg.AllowedCommands)
	var actuator cmdexec.Actuator
	switch cfg.CommandMode {
	case "simulate":
		actuator = &cmdexec.Simulated{}
		log.Printf("command mode: SIMULATE - no hardware will be actuated")
	case "modbus":
		// Real writes. Still behind approval + four-eyes on the server, the
		// gate's allowlist (modbus.write must be listed) and per-device `writes`.
		actuator = &modbusActuator{reg: writers}
		log.Printf("command mode: MODBUS - allowlisted registers can be written after four-eyes approval")
	}
	var mcp atomic.Pointer[mqttc.Client]
	onCmd := func(_ mqtt.Client, m mqtt.Message) {
		ack, ok := cmdexec.Handle(ctx, gate, actuator, m.Payload(), time.Now())
		log.Printf("cmd %s: %s %s", ack.RequestID, ack.State, ack.Detail)
		mc := mcp.Load()
		if !ok || mc == nil {
			return
		}
		b, _ := json.Marshal(ack)
		if err := mc.Publish("t/"+cfg.TenantID+"/g/"+cfg.GatewayID+"/cmd/ack", b); err != nil {
			log.Printf("cmd %s: ack publish: %v", ack.RequestID, err)
		}
	}

	tracker := localui.NewTracker()
	startLocalUI(ctx, cfg, q, func() bool { c := mcp.Load(); return c != nil && c.Connected() }, tracker)

	// Edge-local alarm rules run independently of the broker connection, so a
	// siren still sounds if the server or network is gone (also at boot).
	rulesEng := startLocalRules(ctx, cfg, *cfgPath, *identityDir, func() bool { c := mcp.Load(); return c != nil && c.Connected() })
	_ = rulesEng

	telemetryTopic := "t/" + cfg.TenantID + "/g/" + cfg.GatewayID + "/telemetry"
	diagTopic := "t/" + cfg.TenantID + "/g/" + cfg.GatewayID + "/diag"

	// Poll loops run under a supervisor so fleet config applies can reload
	// them without restarting the agent (or dropping the MQTT connection).
	sup := startSupervisor(cfg, q, telemetryTopic, tracker)
	defer sup.stop()

	// The broker connection comes up in the background: polling, buffering and
	// local rules must not wait for the server.
	go func() {
		var mc *mqttc.Client
		for {
			c, err := mqttc.Connect(cfg, onCmd)
			if err == nil {
				mc = c
				break
			}
			log.Printf("mqtt: %v (retrying in 30s; running offline)", err)
			select {
			case <-ctx.Done():
				return
			case <-time.After(30 * time.Second):
			}
		}
		mcp.Store(mc)
		// Fleet manifests: retained release assignments. Verify the staged
		// artifact, then ACK (or fail with the exact reason) on fleet/ack.
		fleetTopic := "t/" + cfg.TenantID + "/g/" + cfg.GatewayID + "/fleet"
		artifactDir := cfg.ArtifactDir
		if artifactDir == "" {
			artifactDir = paths.Artifacts()
		}
		if err := mc.Subscribe(fleetTopic, func(_ mqtt.Client, m mqtt.Message) {
			ack := fleetctl.HandleManifest(m.Payload(), artifactDir, cfg.Serial)
			if ack.State == "acked" {
				// Verified: apply the config artifact with backup + health check +
				// automatic rollback. The supervisor restarts poll loops on the
				// new config; any failure restores the previous one.
				var mfst fleetctl.Manifest
				if json.Unmarshal(m.Payload(), &mfst) == nil && mfst.ArtifactSHA256 != nil {
					ack = applyFleetConfig(mfst, artifactDir, *cfgPath, cfg, sup, q, telemetryTopic, tracker)
				}
			}
			b, _ := json.Marshal(ack)
			if err := mc.Publish(fleetTopic+"/ack", b); err != nil {
				log.Printf("fleet: ack publish: %v", err)
				return
			}
			log.Printf("fleet: campaign %s -> %s (%s)", ack.CampaignID, ack.State, ack.Detail)
		}); err != nil {
			log.Printf("fleet subscribe: %v", err)
		}

		// Commissioning probes: read-only port tests, answered on diag/result.
		// Runs inline; a probe is a single register read with a bounded timeout.
		if err := mc.Subscribe(diagTopic, func(_ mqtt.Client, m mqtt.Message) {
			var req driver.ProbeRequest
			if err := json.Unmarshal(m.Payload(), &req); err != nil {
				log.Printf("diag: bad payload: %v", err)
				return
			}
			timeout := time.Duration(req.TimeoutMs) * time.Millisecond
			if timeout <= 0 || timeout > 30*time.Second {
				timeout = 3 * time.Second
			}
			pctx, cancel := context.WithTimeout(ctx, timeout)
			res := driver.RunProbe(pctx, nil, req)
			cancel()
			b, _ := json.Marshal(res)
			if err := mc.Publish(diagTopic+"/result", b); err != nil {
				log.Printf("diag: result publish: %v", err)
				return
			}
			log.Printf("diag: probe for session %s: ok=%v (%dms)", res.SessionID, res.OK, res.LatencyMs)
		}); err != nil {
			log.Printf("diag subscribe: %v", err)
		}

		// Drain loop: publish buffered items, delete only after broker ACK.
		go func() {
			t := time.NewTicker(2 * time.Second)
			defer t.Stop()
			for {
				select {
				case <-ctx.Done():
					return
				case <-t.C:
					items, err := q.Next(ctx, 100)
					if err != nil {
						log.Printf("queue next: %v", err)
						continue
					}
					for _, it := range items {
						if err := mc.Publish(it.Topic, it.Payload); err != nil {
							log.Printf("publish: %v (keeping %d buffered)", err, it.ID)
							break
						}
						if err := q.Ack(ctx, it.ID); err != nil {
							log.Printf("queue ack: %v", err)
						}
					}
				}
			}
		}()

	}()
	defer func() {
		if c := mcp.Load(); c != nil {
			c.Close()
		}
	}()

	log.Printf("edge-agent up: gateway=%s tenant=%s devices=%d", cfg.GatewayID, cfg.TenantID, len(cfg.Devices))
	<-ctx.Done()
	log.Printf("shutting down")
}

// supervisor owns the per-device poll goroutines for one loaded config.
var writers = &writerRegistry{}

type supervisor struct {
	cancel context.CancelFunc
	done   chan struct{}
}

func startSupervisor(cfg *config.Config, q *queue.Queue, telemetryTopic string, tr *localui.Tracker) *supervisor {
	tr.Reset()
	writers.reset()
	ctx, cancel := context.WithCancel(context.Background())
	s := &supervisor{cancel: cancel, done: make(chan struct{})}
	go func() {
		defer close(s.done)
		var wg sync.WaitGroup
		for _, dev := range cfg.Devices {
			dev := dev
			tr.Register(dev.ID, dev.Profile)
			d, err := driver.New(dev)
			if err != nil {
				tr.RecordError(dev.ID, dev.Profile, err)
				log.Printf("device %s: %v (will retry next reload)", dev.ID, err)
				continue
			}
			defer d.Close()
			if w, ok := d.(driver.Writer); ok && len(dev.Writes) > 0 {
				writers.set(dev.ID, w)
			}
			wg.Add(1)
			go func() {
				defer wg.Done()
				t := time.NewTicker(dev.Interval)
				defer t.Stop()
				for {
					select {
					case <-ctx.Done():
						return
					case <-t.C:
						readings, err := d.Poll(ctx)
						if err != nil {
							log.Printf("poll %s: %v", dev.ID, err)
							tr.RecordError(dev.ID, dev.Profile, err)
							continue
						}
						vals := map[string]float64{}
						for _, r := range readings {
							vals[r.PointID] = r.Value
						}
						tr.RecordRead(dev.ID, dev.Profile, vals)
						if e := ruleObs.Load(); e != nil {
							e.Observe(dev.ID, vals)
						}
						for _, r := range readings {
							e := envelope{
								EventID: uuid.NewString(), TenantID: cfg.TenantID,
								GatewayID: cfg.GatewayID, DeviceID: r.DeviceID, PointID: r.PointID,
								ObservedAt: time.Now().UTC().Format(time.RFC3339Nano),
								Value:      r.Value, Unit: r.Unit, Quality: "measured", SchemaVersion: 1,
							}
							b, _ := json.Marshal(e)
							if err := q.Put(ctx, telemetryTopic, b); err != nil {
								log.Printf("queue put: %v", err)
							}
						}
					}
				}
			}()
		}
		wg.Wait()
	}()
	log.Printf("supervisor up: %d devices", len(cfg.Devices))
	return s
}

func (s *supervisor) stop() {
	s.cancel()
	<-s.done
}

// applyFleetConfig installs a verified config artifact and restarts the poll
// supervisor on it. Identity is pinned: a config naming a different gateway
// or tenant is refused. Any install/validation failure rolls back
// byte-for-byte and restarts the old loops; the ACK reports the outcome.
func applyFleetConfig(m fleetctl.Manifest, artifactDir, cfgPath string, cfg *config.Config, sup *supervisor, q *queue.Queue, telemetryTopic string, tracker *localui.Tracker) fleetctl.Ack {
	ack := fleetctl.Ack{CampaignID: m.CampaignID}
	artPath, err := fleetctl.ArtifactPath(artifactDir, *m.ArtifactSHA256)
	if err != nil {
		ack.State, ack.Detail = "failed", err.Error()
		return ack
	}
	newCfg, err := config.Load(artPath)
	if err != nil {
		ack.State, ack.Detail = "failed", "new config invalid: "+err.Error()
		return ack
	}
	if newCfg.GatewayID != cfg.GatewayID || newCfg.TenantID != cfg.TenantID {
		ack.State = "failed"
		ack.Detail = "config identity mismatch: refusing another gateway's config"
		return ack
	}

	if err := validateRulesFor(newCfg, cfgPath); err != nil {
		ack.State, ack.Detail = "failed", "new config's local rules invalid: "+err.Error()
		return ack
	}
	sup.stop()
	res := fleetctl.ApplyConfig(artPath, cfgPath)
	if !res.Applied {
		// rollback supervisor to the (untouched) old config
		*sup = *startSupervisor(cfg, q, telemetryTopic, tracker)
		ack.State = "failed"
		ack.Detail = fmt.Sprintf("release %s apply failed, rolled back: %v", m.Version, res.Err)
		return ack
	}
	// health check: the installed config loads and its supervisor starts
	installed, err := config.Load(cfgPath)
	if err != nil || len(installed.Devices) != res.Devices {
		if rbErr := fleetctl.Rollback(cfgPath, res.BackupPath); rbErr != nil {
			log.Printf("fleet: ROLLBACK FAILED: %v", rbErr)
		}
		*sup = *startSupervisor(cfg, q, telemetryTopic, tracker)
		ack.State = "failed"
		ack.Detail = fmt.Sprintf("release %s failed health check, rolled back", m.Version)
		return ack
	}
	*cfg = *installed
	*sup = *startSupervisor(cfg, q, telemetryTopic, tracker)
	if _, err := restartLocalRules(cfg); err != nil {
		log.Printf("fleet: local rules restart: %v", err)
	}
	ack.State = "acked"
	ack.Detail = fmt.Sprintf("release %s applied: %d devices polling (backup %s)", m.Version, res.Devices, res.BackupPath)
	return ack
}

// startLocalUI serves the read-only status page unless disabled in config.
func startLocalUI(ctx context.Context, cfg *config.Config, q *queue.Queue, connected func() bool, tr *localui.Tracker) {
	addr := cfg.UI.Listen
	if addr == "off" {
		return
	}
	if addr == "" {
		addr = "127.0.0.1:8088"
	}
	h := localui.Handler(localui.Info{
		GatewayID: cfg.GatewayID, TenantID: cfg.TenantID, Version: version,
		BrokerHost: fmt.Sprintf("%s:%d", cfg.MQTT.Host, cfg.MQTT.Port), BrokerTLS: cfg.MQTT.TLS,
		Connected: connected, QueueDepth: q.Depth,
	}, tr)
	go func() {
		if err := localui.Serve(ctx, addr, h); err != nil {
			log.Printf("local ui: %v (status page disabled)", err)
		}
	}()
	log.Printf("local status page on http://%s", addr)
}
