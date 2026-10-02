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
	"time"

	"github.com/abhisekrath00-ux/iot-platform/edge/internal/claim"
	"github.com/abhisekrath00-ux/iot-platform/edge/internal/cmdexec"
	"github.com/abhisekrath00-ux/iot-platform/edge/internal/config"
	"github.com/abhisekrath00-ux/iot-platform/edge/internal/driver"
	"github.com/abhisekrath00-ux/iot-platform/edge/internal/fleetctl"
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
	identityDir := flag.String("identity-dir", paths.Data(), "directory holding identity.json")
	flag.Parse()

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
	var mc *mqttc.Client
	onCmd := func(_ mqtt.Client, m mqtt.Message) {
		ack, ok := cmdexec.Handle(ctx, gate, actuator, m.Payload(), time.Now())
		log.Printf("cmd %s: %s %s", ack.RequestID, ack.State, ack.Detail)
		if !ok || mc == nil {
			return
		}
		b, _ := json.Marshal(ack)
		if err := mc.Publish("t/"+cfg.TenantID+"/g/"+cfg.GatewayID+"/cmd/ack", b); err != nil {
			log.Printf("cmd %s: ack publish: %v", ack.RequestID, err)
		}
	}

	mc, err = mqttc.Connect(cfg, onCmd)
	if err != nil {
		log.Fatalf("mqtt: %v", err)
	}
	defer mc.Close()

	tracker := localui.NewTracker()
	startLocalUI(ctx, cfg, q, mc, tracker)

	telemetryTopic := "t/" + cfg.TenantID + "/g/" + cfg.GatewayID + "/telemetry"
	diagTopic := "t/" + cfg.TenantID + "/g/" + cfg.GatewayID + "/diag"

	// Poll loops run under a supervisor so fleet config applies can reload
	// them without restarting the agent (or dropping the MQTT connection).
	sup := startSupervisor(cfg, q, telemetryTopic, tracker)
	defer sup.stop()

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
		log.Fatalf("fleet subscribe: %v", err)
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
		log.Fatalf("diag subscribe: %v", err)
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
	ack.State = "acked"
	ack.Detail = fmt.Sprintf("release %s applied: %d devices polling (backup %s)", m.Version, res.Devices, res.BackupPath)
	return ack
}

// startLocalUI serves the read-only status page unless disabled in config.
func startLocalUI(ctx context.Context, cfg *config.Config, q *queue.Queue, mc *mqttc.Client, tr *localui.Tracker) {
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
		Connected: mc.Connected, QueueDepth: q.Depth,
	}, tr)
	go func() {
		if err := localui.Serve(ctx, addr, h); err != nil {
			log.Printf("local ui: %v (status page disabled)", err)
		}
	}()
	log.Printf("local status page on http://%s", addr)
}
