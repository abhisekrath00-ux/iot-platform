package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log"
	"math"
	"time"

	"github.com/abhisekrath00-ux/iot-platform/server/internal/flow"
	"github.com/abhisekrath00-ux/iot-platform/server/internal/flow/jsfn"
	"github.com/abhisekrath00-ux/iot-platform/server/internal/notify"
	"github.com/abhisekrath00-ux/iot-platform/server/internal/rules"
	"github.com/abhisekrath00-ux/iot-platform/server/internal/sparkplug"
	"github.com/abhisekrath00-ux/iot-platform/server/internal/store"
	mqtt "github.com/eclipse/paho.mqtt.golang"
)

// Sparkplug B bridge. Sparkplug topics carry no tenant, so the bridge is off
// unless SPARKPLUG_TENANT names the single tenant it feeds, and the broker ACL
// must restrict who may publish under spBv1.0/ (see docs/connectors.md).
// Not a shared subscription: every replica must see BIRTH messages for its alias table; duplicates are
// harmless because event ids are deterministic. Platform device id = "<edge_node>" for node metrics or "<edge_node>:<device>";
// point id = the metric name. Both must already exist (onboarded in the UI):
// unknown devices/points are dropped, values are range-checked.

type spReading struct {
	DeviceID, PointID, EventID string
	At                         time.Time
	Value                      float64
}

func sparkplugDeviceID(t sparkplug.Topic) string {
	if t.Device == "" {
		return t.EdgeNode
	}
	return t.EdgeNode + ":" + t.Device
}

// sparkplugReadings turns one message into readings. Pure: no I/O.
func sparkplugReadings(topic string, payload []byte, al *sparkplug.Aliases, now time.Time) (string, []spReading, error) {
	t, err := sparkplug.ParseTopic(topic)
	if err != nil {
		return "", nil, err
	}
	pl, err := sparkplug.Decode(payload)
	if err != nil {
		return "", nil, err
	}
	dev := sparkplugDeviceID(t)
	var out []spReading
	for _, m := range al.Resolve(t, pl) {
		if !m.Numeric || m.IsNull || m.IsHistorical || len(m.Name) > 200 {
			continue
		}
		ms := m.TimestampMs
		if ms == 0 {
			ms = pl.TimestampMs
		}
		at := now
		if ms != 0 {
			if ms > math.MaxInt64/2 {
				continue
			}
			at = time.UnixMilli(int64(ms)).UTC()
		}
		if at.After(now.Add(5*time.Minute)) || now.Sub(at) > 30*24*time.Hour {
			continue
		}
		h := sha256.Sum256([]byte(fmt.Sprintf("sp|%s|%s|%d|%d|%v", topic, m.Name, at.UnixNano(), pl.Seq, m.Value)))
		out = append(out, spReading{DeviceID: dev, PointID: m.Name, At: at, Value: m.Value, EventID: "sp-" + hex.EncodeToString(h[:12])})
	}
	return dev, out, nil
}

func startSparkplug(ctx context.Context, c mqtt.Client, st *store.Store, tenant string, n *notify.Notifier) {
	al := &sparkplug.Aliases{}
	handler := func(_ mqtt.Client, m mqtt.Message) {
		dev, rs, err := sparkplugReadings(m.Topic(), m.Payload(), al, time.Now())
		if err != nil {
			log.Printf("sparkplug drop %q: %v", m.Topic(), err)
			return
		}
		if len(rs) == 0 {
			return
		}
		var gw string
		if err := st.Pool.QueryRow(ctx, `SELECT gateway_id FROM devices WHERE id=$1 AND tenant_id=$2`, dev, tenant).Scan(&gw); err != nil {
			log.Printf("sparkplug drop: device %q is not onboarded for tenant %s", dev, tenant)
			return
		}
		for _, r := range rs {
			var unit string
			var min, max *float64
			if err := st.Pool.QueryRow(ctx, `SELECT unit, min_value, max_value FROM points WHERE device_id=$1 AND id=$2`, dev, r.PointID).Scan(&unit, &min, &max); err != nil {
				continue // metric not registered as a point
			}
			if (min != nil && r.Value < *min) || (max != nil && r.Value > *max) {
				log.Printf("sparkplug drop: %s.%s=%v out of range", dev, r.PointID, r.Value)
				continue
			}
			if err := st.InsertTelemetry(ctx, store.Telemetry{EventID: r.EventID, TenantID: tenant, GatewayID: gw, DeviceID: dev, PointID: r.PointID,
				ObservedAt: r.At, Value: r.Value, Unit: unit, Quality: "measured", SchemaVersion: 1}); err != nil {
				log.Printf("sparkplug insert: %v", err)
				continue
			}
			rules.Evaluate(ctx, st.Pool, n, tenant, dev, r.PointID, r.Value)
			flow.EvaluateWith(ctx, st.Pool, n, jsfn.New(4), tenant, dev, r.PointID, r.Value)
		}
	}
	for _, f := range []string{"spBv1.0/+/NBIRTH/+", "spBv1.0/+/NDATA/+", "spBv1.0/+/NDEATH/+",
		"spBv1.0/+/DBIRTH/+/+", "spBv1.0/+/DDATA/+/+", "spBv1.0/+/DDEATH/+/+"} {
		if tok := c.Subscribe(f, 1, handler); tok.Wait() && tok.Error() != nil {
			log.Fatalf("sparkplug subscribe %s: %v", f, tok.Error())
		}
	}
	log.Printf("sparkplug B bridge on, tenant=%s", tenant)
}
