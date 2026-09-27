// edge-agent runs on the gateway SBC: polls configured serial devices,
// buffers validated readings in SQLite, publishes over MQTT, and executes
// only allowlisted, approved, unexpired commands.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"log"
	"os"
	"os/signal"
	"time"

	"github.com/abhisekrath00-ux/iot-platform/edge/internal/config"
	"github.com/abhisekrath00-ux/iot-platform/edge/internal/driver"
	"github.com/abhisekrath00-ux/iot-platform/edge/internal/mqttc"
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

func main() {
	cfgPath := flag.String("config", "/etc/hexmon/edge-agent.yaml", "config file")
	flag.Parse()

	cfg, err := config.Load(*cfgPath)
	if err != nil {
		log.Fatalf("config: %v", err)
	}
	q, err := queue.Open(cfg.QueuePath)
	if err != nil {
		log.Fatalf("queue: %v", err)
	}
	defer q.Close()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	allowed := map[string]bool{}
	for _, a := range cfg.AllowedCommands {
		allowed[a] = true
	}
	onCmd := func(_ mqtt.Client, m mqtt.Message) {
		var c command
		if err := json.Unmarshal(m.Payload(), &c); err != nil {
			log.Printf("cmd: bad payload: %v", err)
			return
		}
		if !allowed[c.Action] {
			log.Printf("cmd %s: action %q not in allowlist, rejected", c.RequestID, c.Action)
			return
		}
		if time.Now().After(c.ExpiresAt) {
			log.Printf("cmd %s: expired, rejected", c.RequestID)
			return
		}
		// Execution adapters land here per actuator driver, with ACK publish
		// and measured outcome. Physical actuation requires the safety gating
		// in docs/security.md before merge.
		log.Printf("cmd %s: %s accepted (executor not yet implemented)", c.RequestID, c.Action)
	}

	mc, err := mqttc.Connect(cfg, onCmd)
	if err != nil {
		log.Fatalf("mqtt: %v", err)
	}
	defer mc.Close()

	telemetryTopic := "t/" + cfg.TenantID + "/g/" + cfg.GatewayID + "/telemetry"

	// Poll loops, one per device.
	for _, dev := range cfg.Devices {
		dev := dev
		d, err := driver.New(dev)
		if err != nil {
			log.Printf("device %s: %v (will retry next start)", dev.ID, err)
			continue
		}
		defer d.Close()
		go func() {
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
						continue
					}
					for _, r := range readings {
						e := envelope{
							EventID: uuid.NewString(), TenantID: cfg.TenantID,
							GatewayID: cfg.GatewayID, DeviceID: r.DeviceID, PointID: r.PointID,
							ObservedAt: time.Now().UTC().Format(time.RFC3339Nano),
							Value: r.Value, Unit: r.Unit, Quality: "measured", SchemaVersion: 1,
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
