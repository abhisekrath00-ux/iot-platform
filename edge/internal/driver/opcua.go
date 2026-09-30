package driver

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/abhisekrath00-ux/iot-platform/edge/internal/config"
	"github.com/gopcua/opcua"
	"github.com/gopcua/opcua/ua"
)

// opcuaDriver reads OPC UA nodes (SCADA servers, PLCs with embedded OPC UA:
// Siemens, Beckhoff, Allen-Bradley, Kepware, Ignition, open62541...).
// Each point names a NodeID; all points are read in one batched request.
type opcuaDriver struct {
	dev    config.Device
	client *opcua.Client
	ids    []*ua.NodeID
}

func newOPCUA(d config.Device) (Driver, error) {
	ep := d.Endpoint
	if ep == "" && d.Host != "" {
		port := d.NetPort
		if port == 0 {
			port = 4840
		}
		ep = "opc.tcp://" + d.Host + ":" + strconv.Itoa(port)
	}
	if ep == "" {
		return nil, fmt.Errorf("device %s: endpoint or host required for opcua", d.ID)
	}
	o := &opcuaDriver{dev: d}
	for _, p := range d.Points {
		if p.NodeID == "" {
			return nil, fmt.Errorf("device %s point %s: node_id required", d.ID, p.ID)
		}
		id, err := ua.ParseNodeID(p.NodeID)
		if err != nil {
			return nil, fmt.Errorf("device %s point %s: bad node_id %q: %w", d.ID, p.ID, p.NodeID, err)
		}
		o.ids = append(o.ids, id)
	}
	c, err := opcua.NewClient(ep,
		opcua.SecurityMode(ua.MessageSecurityModeNone),
		opcua.RequestTimeout(3*time.Second),
		opcua.DialTimeout(3*time.Second),
	)
	if err != nil {
		return nil, fmt.Errorf("device %s: opcua client: %w", d.ID, err)
	}
	o.client = c
	return o, nil
}

func (o *opcuaDriver) connect(ctx context.Context) error {
	if o.client.State() == opcua.Connected {
		return nil
	}
	return o.client.Connect(ctx)
}

func asFloat(v any) (float64, bool) {
	switch x := v.(type) {
	case float64:
		return x, true
	case float32:
		return float64(x), true
	case int8:
		return float64(x), true
	case int16:
		return float64(x), true
	case int32:
		return float64(x), true
	case int64:
		return float64(x), true
	case uint8:
		return float64(x), true
	case uint16:
		return float64(x), true
	case uint32:
		return float64(x), true
	case uint64:
		return float64(x), true
	case int:
		return float64(x), true
	case bool:
		if x {
			return 1, true
		}
		return 0, true
	}
	return 0, false
}

func (o *opcuaDriver) Poll(ctx context.Context) ([]Reading, error) {
	if err := o.connect(ctx); err != nil {
		return nil, fmt.Errorf("opcua connect: %w", err)
	}
	req := &ua.ReadRequest{MaxAge: 0, TimestampsToReturn: ua.TimestampsToReturnNeither}
	for _, id := range o.ids {
		req.NodesToRead = append(req.NodesToRead, &ua.ReadValueID{NodeID: id, AttributeID: ua.AttributeIDValue})
	}
	resp, err := o.client.Read(ctx, req)
	if err != nil {
		o.client.Close(ctx) // re-dial next poll
		return nil, fmt.Errorf("opcua read: %w", err)
	}
	var out []Reading
	for i, p := range o.dev.Points {
		if i >= len(resp.Results) {
			break
		}
		r := resp.Results[i]
		if r.Status != ua.StatusOK || r.Value == nil {
			return out, fmt.Errorf("point %s: status %v", p.ID, r.Status)
		}
		v, ok := asFloat(r.Value.Value())
		if !ok {
			return out, fmt.Errorf("point %s: non-numeric value %T", p.ID, r.Value.Value())
		}
		scale := p.Scale
		if scale == 0 {
			scale = 1
		}
		v *= scale
		if v < p.Min || v > p.Max {
			return out, fmt.Errorf("point %s: value %v outside [%v,%v]", p.ID, v, p.Min, p.Max)
		}
		out = append(out, Reading{DeviceID: o.dev.ID, PointID: p.ID, Value: v, Unit: p.Unit})
	}
	return out, nil
}

func (o *opcuaDriver) Close() error {
	return o.client.Close(context.Background())
}
