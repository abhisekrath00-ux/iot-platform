// ingest subscribes to tenant telemetry topics, validates envelopes and
// writes to Postgres. Idempotent via event_id.
package main

import (
	"context"
	"encoding/json"
	"log"
	"os"
	"os/signal"
	"time"

	mqtt "github.com/eclipse/paho.mqtt.golang"
	"github.com/abhisekrath00-ux/iot-platform/server/internal/notify"
	"github.com/abhisekrath00-ux/iot-platform/server/internal/rules"
	"github.com/abhisekrath00-ux/iot-platform/server/internal/store"
)

type envelope struct {
	EventID       string    `json:"event_id"`
	TenantID      string    `json:"tenant_id"`
	GatewayID     string    `json:"gateway_id"`
	DeviceID      string    `json:"device_id"`
	PointID       string    `json:"point_id"`
	ObservedAt    time.Time `json:"observed_at"`
	Value         float64   `json:"value"`
	Unit          string    `json:"unit"`
	Quality       string    `json:"quality"`
	SchemaVersion int       `json:"schema_version"`
}

func main() {
	dbURL := mustEnv("DATABASE_URL")
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	st, err := store.Connect(ctx, dbURL)
	if err != nil {
		log.Fatalf("store: %v", err)
	}
	defer st.Close()

	notifier := notify.FromEnv()

	opts := mqtt.NewClientOptions().
		AddBroker("tcp://"+mustEnv("MQTT_HOST")+":"+envOr("MQTT_PORT", "1883")).
		SetClientID("ingest-1").SetAutoReconnect(true).SetConnectRetry(true)
	// TODO(production): TLS + broker auth from env; see docs/deployment.md.

	c := mqtt.NewClient(opts)
	if tok := c.Connect(); tok.Wait() && tok.Error() != nil {
		log.Fatalf("mqtt connect: %v", tok.Error())
	}

	handler := func(_ mqtt.Client, m mqtt.Message) {
		var e envelope
		if err := json.Unmarshal(m.Payload(), &e); err != nil {
			log.Printf("drop: bad envelope: %v", err)
			return
		}
		if e.SchemaVersion != 1 || e.EventID == "" || e.TenantID == "" || e.DeviceID == "" || e.PointID == "" {
			log.Printf("drop: invalid envelope fields (event=%s)", e.EventID)
			return
		}
		if e.ObservedAt.After(time.Now().Add(5*time.Minute)) || time.Since(e.ObservedAt) > 30*24*time.Hour {
			log.Printf("drop: implausible observed_at %s (event=%s)", e.ObservedAt, e.EventID)
			return
		}
		// TODO: enforce tenant from authenticated broker identity mapping
		// (client cert CN -> tenant), never from payload alone.
		if e.Quality == "" {
			e.Quality = "measured"
		}
		err := st.InsertTelemetry(ctx, store.Telemetry{
			EventID: e.EventID, TenantID: e.TenantID, GatewayID: e.GatewayID,
			DeviceID: e.DeviceID, PointID: e.PointID, ObservedAt: e.ObservedAt,
			Value: e.Value, Unit: e.Unit, Quality: e.Quality, SchemaVersion: e.SchemaVersion,
		})
		if err != nil {
			log.Printf("insert %s: %v", e.EventID, err)
			return
		}
		rules.Evaluate(ctx, st.Pool, notifier, e.TenantID, e.DeviceID, e.PointID, e.Value)
	}

	if tok := c.Subscribe("t/+/g/+/telemetry", 1, handler); tok.Wait() && tok.Error() != nil {
		log.Fatalf("subscribe: %v", tok.Error())
	}
	log.Printf("ingest up")
	<-ctx.Done()
	c.Disconnect(250)
}

func mustEnv(k string) string {
	v := os.Getenv(k)
	if v == "" {
		log.Fatalf("missing env %s", k)
	}
	return v
}
func envOr(k, d string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return d
}
