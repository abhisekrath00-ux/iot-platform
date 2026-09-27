// ingest subscribes to tenant telemetry topics, validates envelopes and
// writes to Postgres. Idempotent via event_id.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strings"
	"time"

	"github.com/abhisekrath00-ux/iot-platform/server/internal/flow"
	"github.com/abhisekrath00-ux/iot-platform/server/internal/notify"
	"github.com/abhisekrath00-ux/iot-platform/server/internal/rules"
	"github.com/abhisekrath00-ux/iot-platform/server/internal/store"
	mqtt "github.com/eclipse/paho.mqtt.golang"
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
		AddBroker("tcp://" + mustEnv("MQTT_HOST") + ":" + envOr("MQTT_PORT", "1883")).
		SetClientID("ingest-1").SetAutoReconnect(true).SetConnectRetry(true)
	// TODO(production): TLS + broker auth from env; see docs/deployment.md.

	c := mqtt.NewClient(opts)
	if tok := c.Connect(); tok.Wait() && tok.Error() != nil {
		log.Fatalf("mqtt connect: %v", tok.Error())
	}

	handler := func(_ mqtt.Client, m mqtt.Message) {
		e, err := resolveEnvelope(m.Topic(), m.Payload(), time.Now())
		if err != nil {
			log.Printf("drop: %v", err)
			return
		}
		err = st.InsertTelemetry(ctx, store.Telemetry{
			EventID: e.EventID, TenantID: e.TenantID, GatewayID: e.GatewayID,
			DeviceID: e.DeviceID, PointID: e.PointID, ObservedAt: e.ObservedAt,
			Value: e.Value, Unit: e.Unit, Quality: e.Quality, SchemaVersion: e.SchemaVersion,
		})
		if err != nil {
			log.Printf("insert %s: %v", e.EventID, err)
			return
		}
		rules.Evaluate(ctx, st.Pool, notifier, e.TenantID, e.DeviceID, e.PointID, e.Value)
		flow.Evaluate(ctx, st.Pool, notifier, e.TenantID, e.DeviceID, e.PointID, e.Value)
	}

	if tok := c.Subscribe("t/+/g/+/telemetry", 1, handler); tok.Wait() && tok.Error() != nil {
		log.Fatalf("subscribe: %v", tok.Error())
	}
	log.Printf("ingest up")
	<-ctx.Done()
	c.Disconnect(250)
}

// resolveEnvelope validates a telemetry message and returns the envelope with
// tenant and gateway identity taken from the broker-enforced topic path,
// never from payload fields. Under the production broker (mTLS with
// use_identity_as_username and per-gateway ACL subtrees, see
// deploy/mosquitto-mtls.conf) a gateway can only publish inside
// t/<tenant>/g/<serial>/..., so the topic carries the authenticated identity.
// Payload identity fields may confirm the topic identity but never override
// it; a mismatch means the payload is lying and the message is dropped.
func resolveEnvelope(topic string, payload []byte, now time.Time) (envelope, error) {
	parts := strings.Split(topic, "/")
	if len(parts) != 5 || parts[0] != "t" || parts[2] != "g" || parts[4] != "telemetry" || parts[1] == "" || parts[3] == "" {
		return envelope{}, fmt.Errorf("bad topic %q", topic)
	}
	topicTenant, topicGW := parts[1], parts[3]

	var e envelope
	if err := json.Unmarshal(payload, &e); err != nil {
		return envelope{}, fmt.Errorf("bad envelope json: %w", err)
	}
	if e.TenantID != "" && e.TenantID != topicTenant {
		return envelope{}, fmt.Errorf("payload tenant %q does not match topic tenant %q (spoof attempt, event=%s)", e.TenantID, topicTenant, e.EventID)
	}
	if e.GatewayID != "" && e.GatewayID != topicGW {
		return envelope{}, fmt.Errorf("payload gateway %q does not match topic gateway %q (spoof attempt, event=%s)", e.GatewayID, topicGW, e.EventID)
	}
	e.TenantID, e.GatewayID = topicTenant, topicGW

	if e.SchemaVersion != 1 || e.EventID == "" || e.DeviceID == "" || e.PointID == "" {
		return envelope{}, fmt.Errorf("invalid envelope fields (event=%s)", e.EventID)
	}
	if e.ObservedAt.After(now.Add(5*time.Minute)) || now.Sub(e.ObservedAt) > 30*24*time.Hour {
		return envelope{}, fmt.Errorf("implausible observed_at %s (event=%s)", e.ObservedAt, e.EventID)
	}
	if e.Quality == "" {
		e.Quality = "measured"
	}
	return e, nil
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
