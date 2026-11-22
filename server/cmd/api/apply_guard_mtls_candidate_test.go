package main

import (
	"encoding/json"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"github.com/abhisekrath00-ux/iot-platform/server/internal/mqttconfig"
	"github.com/abhisekrath00-ux/iot-platform/server/internal/store"
	mqtt "github.com/eclipse/paho.mqtt.golang"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Combines two unwired candidates ONLY in a disposable private schema with
// generated mTLS identities and a read-only observer, never an edge executor.
func verifyApplyGuardKnownPublicationMTLS(t *testing.T, port, ca, gc, gk, ac, ak string) {
	t.Helper()
	s, m, tenant, path := applyGuardFixtureTenant(t, "itest-mqpipe")
	if err := installApplyGuard(t, s); err != nil {
		t.Fatal(err)
	}
	options, err := mqttconfig.Options(func(k string) string {
		return map[string]string{"MQTT_TLS": "true", "MQTT_HOST": "127.0.0.1", "MQTT_PORT": port, "MQTT_CA_FILE": ca, "MQTT_CERT_FILE": gc, "MQTT_KEY_FILE": gk}[k]
	}, "apply-guard-known-ack-observer")
	if err != nil {
		t.Fatal(err)
	}
	observer := mqtt.NewClient(options)
	tok := observer.Connect()
	if !tok.WaitTimeout(5*time.Second) || tok.Error() != nil {
		t.Fatal("connect", tok.Error())
	}
	defer observer.Disconnect(100)
	var deliveries, calls atomic.Int32
	seen := make(chan configPushEnvelope, 4)
	topic := "t/itest-mqpipe/g/itest-mqpipe-gw/config"
	tok = observer.Subscribe(topic, 1, func(_ mqtt.Client, msg mqtt.Message) {
		var e configPushEnvelope
		if json.Unmarshal(msg.Payload(), &e) == nil && e.PushID != "" {
			deliveries.Add(1)
			select {
			case seen <- e:
			default:
			}
		}
	})
	if !tok.WaitTimeout(5*time.Second) || tok.Error() != nil {
		t.Fatal("subscribe", tok.Error())
	}
	for k, v := range map[string]string{"MQTT_TLS": "true", "MQTT_HOST": "127.0.0.1", "MQTT_PORT": port, "MQTT_CA_FILE": ca, "MQTT_CERT_FILE": ac, "MQTT_KEY_FILE": ak} {
		t.Setenv(k, v)
	}
	s.opsPublishHook = func(got string, b []byte) error {
		calls.Add(1)
		if got != topic {
			t.Errorf("wrong topic: %s", got)
		}
		return publishApplyMQTTOnceCandidate(t.Context(), got, b)
	}
	first := call(m, tenant, "admin", "POST", path, `{}`)
	if first.Code != 202 {
		t.Fatal("actual broker ACK/handler", first.Code, first.Body.String())
	}
	var e configPushEnvelope
	select {
	case e = <-seen:
	case <-time.After(3 * time.Second):
		t.Fatal("signed envelope not observed")
	}
	if e.TenantID != tenant || e.Signature == "" {
		t.Fatal("envelope identity/signature missing")
	}
	if _, err := s.st.Pool.Exec(t.Context(), `UPDATE gateway_config_pushes SET created_at=now()-interval '1 day' WHERE id=$1`, e.PushID); err != nil {
		t.Fatal(err)
	}
	pool, err := pgxpool.NewWithConfig(t.Context(), s.st.Pool.Config())
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	fresh := &server{st: &store.Store{Pool: pool}, opsPublishHook: s.opsPublishHook}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/gateways/{id}/config-push", fresh.listConfigPushes)
	mux.HandleFunc("POST /v1/gateways/{id}/config-push", fresh.pushGatewayConfig)
	listed := call(mux, tenant, "admin", "GET", path, "")
	next := call(mux, tenant, "admin", "POST", path, `{}`)
	var rows []struct {
		ID     string `json:"id"`
		Status string `json:"status"`
	}
	if listed.Code != 200 || json.Unmarshal(listed.Body.Bytes(), &rows) != nil || len(rows) != 1 || rows[0].ID != e.PushID || rows[0].Status != "sent" || next.Code != 409 || calls.Load() != 1 || deliveries.Load() != 1 {
		t.Fatal("known ACK rearmed", listed.Code, listed.Body.String(), next.Code, calls.Load(), deliveries.Load())
	}
	assertGuardLedger(t, s)
	t.Log("ISOLATED_DB_GUARD_AND_ONE_CONNECTION_CANDIDATES_REAL_MTLS_BROKER_ACK_HANDLER202_ONE_SIGNED_OBSERVER_DELIVERY_DAYOLD_NEW_POOL_GET_SENT_POST409_ONE_PUBLISH; no executor, gateway receipt, physical application or activation")
}
