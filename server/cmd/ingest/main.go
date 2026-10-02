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
	"github.com/abhisekrath00-ux/iot-platform/server/internal/flow/jsfn"
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
		SetClientID(clientID()).SetAutoReconnect(true).SetConnectRetry(true)
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
		flow.EvaluateWith(ctx, st.Pool, notifier, jsfn.New(4), e.TenantID, e.DeviceID, e.PointID, e.Value)
	}

	if tok := c.Subscribe(subTopic("t/+/g/+/telemetry"), 1, handler); tok.Wait() && tok.Error() != nil {
		log.Fatalf("subscribe: %v", tok.Error())
	}

	// Commissioning port-test results: the edge answers diag probes on
	// t/<tenant>/g/<gateway>/diag/result. Topic identity is enforced the same
	// way as telemetry: a result only lands on a session whose tenant and
	// gateway match the broker-authenticated topic path.
	diagHandler := func(_ mqtt.Client, m mqtt.Message) {
		parts := strings.Split(m.Topic(), "/")
		if len(parts) != 6 || parts[4] != "diag" || parts[5] != "result" || parts[1] == "" || parts[3] == "" {
			log.Printf("diag drop: bad topic %q", m.Topic())
			return
		}
		var res struct {
			SessionID  string          `json:"session_id"`
			OK         bool            `json:"ok"`
			Error      string          `json:"error"`
			Readings   json.RawMessage `json:"readings"`
			LatencyMs  int64           `json:"latency_ms"`
			FinishedAt time.Time       `json:"finished_at"`
		}
		if err := json.Unmarshal(m.Payload(), &res); err != nil || res.SessionID == "" {
			log.Printf("diag drop: bad payload")
			return
		}
		payload, _ := json.Marshal(res)
		tag, err := st.Pool.Exec(ctx,
			`UPDATE commissioning_sessions
			 SET port_test = COALESCE(port_test,'{}'::jsonb) - 'result' || jsonb_build_object('result', $1::jsonb),
			     state = CASE WHEN $2 THEN 'tested' ELSE 'failed' END,
			     updated_at = now()
			 WHERE id = $3 AND tenant_id = $4 AND gateway_id = $5`,
			payload, res.OK, res.SessionID, parts[1], parts[3])
		if err != nil {
			log.Printf("diag update %s: %v", res.SessionID, err)
			return
		}
		if tag.RowsAffected() == 0 {
			log.Printf("diag drop: no session %s for topic identity %s/%s", res.SessionID, parts[1], parts[3])
		}
	}
	if tok := c.Subscribe(subTopic("t/+/g/+/diag/result"), 1, diagHandler); tok.Wait() && tok.Error() != nil {
		log.Fatalf("diag subscribe: %v", tok.Error())
	}

	// Fleet auto-ACK: the edge verifies the delivered artifact and answers on
	// t/<tenant>/g/<gateway>/fleet/ack. Topic identity pins the ack to the
	// gateway's own assignment - a gateway cannot ack for a neighbor. A
	// failure trips the campaign halt threshold automatically.
	ackHandler := func(_ mqtt.Client, m mqtt.Message) {
		parts := strings.Split(m.Topic(), "/")
		if len(parts) != 6 || parts[4] != "fleet" || parts[5] != "ack" || parts[1] == "" || parts[3] == "" {
			log.Printf("fleet ack drop: bad topic %q", m.Topic())
			return
		}
		var ack struct {
			CampaignID string `json:"campaign_id"`
			State      string `json:"state"`
			Detail     string `json:"detail"`
		}
		if err := json.Unmarshal(m.Payload(), &ack); err != nil || ack.CampaignID == "" {
			log.Printf("fleet ack drop: bad payload")
			return
		}
		if ack.State != "acked" && ack.State != "failed" {
			log.Printf("fleet ack drop: state %q", ack.State)
			return
		}
		tag, err := st.Pool.Exec(ctx,
			`UPDATE fleet_assignments fa SET state=$1, detail=$2, updated_at=now()
			 FROM gateways g
			 WHERE fa.campaign_id=$3 AND fa.gateway_serial=g.serial AND fa.tenant_id=$4
			   AND g.id=$5 AND fa.state IN ('pending','sent')`,
			ack.State, ack.Detail, ack.CampaignID, parts[1], parts[3])
		if err != nil {
			log.Printf("fleet ack update: %v", err)
			return
		}
		if tag.RowsAffected() == 0 {
			log.Printf("fleet ack drop: no open assignment for campaign %s gateway %s", ack.CampaignID, parts[3])
			return
		}
		if ack.State == "failed" {
			// Auto-halt: pause the campaign once failures hit its threshold.
			if _, err := st.Pool.Exec(ctx,
				`UPDATE fleet_campaigns SET state='paused'
				 WHERE id=$1 AND state='running' AND
				   (SELECT count(*) FROM fleet_assignments WHERE campaign_id=$1 AND state='failed') >= failure_threshold`,
				ack.CampaignID); err != nil {
				log.Printf("fleet halt check: %v", err)
			}
		}
	}
	if tok := c.Subscribe(subTopic("t/+/g/+/fleet/ack"), 1, ackHandler); tok.Wait() && tok.Error() != nil {
		log.Fatalf("fleet ack subscribe: %v", tok.Error())
	}
	// Command ACKs from the edge: t/<tenant>/g/<gateway>/cmd/ack. Topic identity
	// pins the ack to the command's own gateway; only 'sent' commands move.
	cmdAckHandler := func(_ mqtt.Client, m mqtt.Message) {
		tenant, gw, ack, err := parseCmdAck(m.Topic(), m.Payload())
		if err != nil {
			log.Printf("cmd ack drop: %v", err)
			return
		}
		state := ack.State
		if state == "rejected" {
			state = "failed"
		}
		outcome, _ := json.Marshal(map[string]any{"edge_state": ack.State, "detail": ack.Detail, "at": ack.At})
		if _, err := st.Pool.Exec(ctx,
			`UPDATE commands SET status=$1, outcome=$2
			 WHERE request_id=$3 AND tenant_id=$4 AND gateway_id=$5 AND status='sent'`,
			state, outcome, ack.RequestID, tenant, gw); err != nil {
			log.Printf("cmd ack update: %v", err)
		}
	}
	if tok := c.Subscribe(subTopic("t/+/g/+/cmd/ack"), 1, cmdAckHandler); tok.Wait() && tok.Error() != nil {
		log.Fatalf("cmd ack subscribe: %v", tok.Error())
	}
	if t := os.Getenv("SPARKPLUG_TENANT"); t != "" {
		startSparkplug(ctx, c, st, t, notifier)
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

// clientID is unique per replica (hostname) so several ingest replicas can
// connect to one broker without kicking each other off.
func clientID() string {
	h, err := os.Hostname()
	if err != nil || h == "" {
		h = "1"
	}
	return "ingest-" + h
}

// subTopic wraps a topic in an MQTT shared subscription when INGEST_SHARED_GROUP
// is set, so N ingest replicas split the load instead of each processing every
// message. Off by default: the broker ACL must allow the $share/<group>/ prefix.
func subTopic(topic string) string {
	if g := os.Getenv("INGEST_SHARED_GROUP"); g != "" {
		return "$share/" + g + "/" + topic
	}
	return topic
}

type cmdAck struct {
	RequestID string    `json:"request_id"`
	State     string    `json:"state"`
	Detail    string    `json:"detail"`
	At        time.Time `json:"at"`
}

// parseCmdAck validates an edge command ack. Identity comes from the topic only.
func parseCmdAck(topic string, payload []byte) (tenant, gateway string, ack cmdAck, err error) {
	parts := strings.Split(topic, "/")
	if len(parts) != 6 || parts[0] != "t" || parts[2] != "g" || parts[4] != "cmd" || parts[5] != "ack" || parts[1] == "" || parts[3] == "" {
		return "", "", ack, fmt.Errorf("bad topic %q", topic)
	}
	if len(payload) > 4096 {
		return "", "", ack, fmt.Errorf("payload too large")
	}
	if err := json.Unmarshal(payload, &ack); err != nil || ack.RequestID == "" {
		return "", "", ack, fmt.Errorf("bad payload")
	}
	switch ack.State {
	case "acked", "failed", "rejected":
	default:
		return "", "", ack, fmt.Errorf("state %q", ack.State)
	}
	if len(ack.Detail) > 500 {
		ack.Detail = ack.Detail[:500]
	}
	return parts[1], parts[3], ack, nil
}
