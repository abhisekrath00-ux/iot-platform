package driver

import (
	"context"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"os"
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
	opts, err := opcuaOptions(d)
	if err != nil {
		return nil, err
	}
	c, err := opcua.NewClient(ep, opts...)
	if err != nil {
		return nil, fmt.Errorf("device %s: opcua client: %w", d.ID, err)
	}
	o.client = c
	return o, nil
}

// opcuaOptions maps the device's security settings to client options.
// Refuses ambiguous setups (encryption without a client cert, username
// without a password source) instead of silently falling back to none.
func opcuaOptions(d config.Device) ([]opcua.Option, error) {
	opts := []opcua.Option{opcua.RequestTimeout(3 * time.Second), opcua.DialTimeout(3 * time.Second)}
	switch d.Security {
	case "", "none":
		opts = append(opts, opcua.SecurityMode(ua.MessageSecurityModeNone))
	case "sign", "sign-and-encrypt":
		if d.ClientCert == "" || d.ClientKey == "" {
			return nil, fmt.Errorf("device %s: security %q needs client_cert and client_key", d.ID, d.Security)
		}
		// The server certificate is pinned: the channel is encrypted to this key,
		// so a server that does not hold the matching private key cannot complete
		// the handshake. Without a pinned cert we would trust whatever the network offers.
		serverDER, err := loadServerCert(d.ServerCert, time.Now())
		if err != nil {
			return nil, fmt.Errorf("device %s: security %q needs server_cert: %w", d.ID, d.Security, err)
		}
		mode := ua.MessageSecurityModeSign
		if d.Security == "sign-and-encrypt" {
			mode = ua.MessageSecurityModeSignAndEncrypt
		}
		opts = append(opts,
			opcua.SecurityPolicy(ua.SecurityPolicyURIBasic256Sha256),
			opcua.SecurityMode(mode),
			opcua.RemoteCertificate(serverDER),
			opcua.CertificateFile(d.ClientCert),
			opcua.PrivateKeyFile(d.ClientKey))
	default:
		return nil, fmt.Errorf("device %s: unknown opcua security %q", d.ID, d.Security)
	}
	if d.Username != "" {
		pw := ""
		if d.PasswordEnv != "" {
			pw = os.Getenv(d.PasswordEnv)
		}
		if pw == "" {
			return nil, fmt.Errorf("device %s: username set but password_env %q is empty", d.ID, d.PasswordEnv)
		}
		opts = append(opts, opcua.AuthUsername(d.Username, pw))
	} else {
		opts = append(opts, opcua.AuthAnonymous())
	}
	return opts, nil
}

// loadServerCert reads the pinned OPC UA server certificate (PEM or DER) and
// refuses a missing, unparseable, not-yet-valid or expired certificate.
func loadServerCert(path string, now time.Time) ([]byte, error) {
	if path == "" {
		return nil, fmt.Errorf("server_cert is empty (export the server's certificate and point server_cert at it)")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	der := raw
	if blk, _ := pem.Decode(raw); blk != nil {
		der = blk.Bytes
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		return nil, fmt.Errorf("server_cert %s is not a valid X.509 certificate: %w", path, err)
	}
	if now.Before(cert.NotBefore) || now.After(cert.NotAfter) {
		return nil, fmt.Errorf("server_cert %s is outside its validity period (%s to %s)", path, cert.NotBefore.Format(time.DateOnly), cert.NotAfter.Format(time.DateOnly))
	}
	return cert.Raw, nil
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
