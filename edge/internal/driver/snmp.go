package driver

import (
	"context"
	"fmt"
	"math"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/abhisekrath00-ux/iot-platform/edge/internal/config"
	"github.com/gosnmp/gosnmp"
)

// snmpDriver reads numeric OIDs from SNMP agents (UPS, PDUs, switches, power
// meters, HVAC, printers). Read-only: there is no SET path. v2c is cleartext
// community auth; prefer v3 authPriv (sha + aes) and keep SNMP on a trusted VLAN.
type snmpDriver struct {
	dev  config.Device
	conn *gosnmp.GoSNMP
	oids []string
}

func newSNMP(d config.Device) (Driver, error) {
	if d.Host == "" {
		return nil, fmt.Errorf("device %s: host required for snmp", d.ID)
	}
	if len(d.Points) == 0 {
		return nil, fmt.Errorf("device %s: no points", d.ID)
	}
	port := d.NetPort
	if port == 0 {
		port = 161
	}
	g := &gosnmp.GoSNMP{Target: d.Host, Port: uint16(port), Timeout: 3 * time.Second, Retries: 1, MaxOids: 40}
	switch d.SNMPVersion {
	case "", "2c":
		g.Version = gosnmp.Version2c
		g.Community = os.Getenv(d.CommunityEnv)
		if d.CommunityEnv == "" || g.Community == "" {
			return nil, fmt.Errorf("device %s: snmp v2c needs community_env naming a set environment variable", d.ID)
		}
	case "3":
		g.Version = gosnmp.Version3
		g.SecurityModel = gosnmp.UserSecurityModel
		g.MsgFlags = gosnmp.AuthPriv
		if d.Username == "" {
			return nil, fmt.Errorf("device %s: snmp v3 needs username", d.ID)
		}
		auth, priv := os.Getenv(d.AuthPassEnv), os.Getenv(d.PrivPassEnv)
		if auth == "" || priv == "" {
			return nil, fmt.Errorf("device %s: snmp v3 authPriv needs snmp_auth_pass_env and snmp_priv_pass_env set", d.ID)
		}
		ap, ok := map[string]gosnmp.SnmpV3AuthProtocol{"sha": gosnmp.SHA, "sha256": gosnmp.SHA256, "sha512": gosnmp.SHA512}[strings.ToLower(d.AuthProto)]
		if !ok {
			return nil, fmt.Errorf("device %s: snmp_auth must be sha, sha256 or sha512 (md5 is refused)", d.ID)
		}
		pp, ok := map[string]gosnmp.SnmpV3PrivProtocol{"aes": gosnmp.AES, "aes256": gosnmp.AES256}[strings.ToLower(d.PrivProto)]
		if !ok {
			return nil, fmt.Errorf("device %s: snmp_priv must be aes or aes256 (des is refused)", d.ID)
		}
		g.SecurityParameters = &gosnmp.UsmSecurityParameters{UserName: d.Username,
			AuthenticationProtocol: ap, AuthenticationPassphrase: auth, PrivacyProtocol: pp, PrivacyPassphrase: priv}
	default:
		return nil, fmt.Errorf("device %s: snmp_version must be 2c or 3", d.ID)
	}
	oids := make([]string, len(d.Points))
	for i, p := range d.Points {
		if !strings.HasPrefix(p.OID, ".1.") && !strings.HasPrefix(p.OID, ".0.") && !strings.HasPrefix(p.OID, ".2.") {
			return nil, fmt.Errorf("device %s point %s: oid must be numeric and start with a dot (e.g. .1.3.6.1.2.1.1.3.0)", d.ID, p.ID)
		}
		oids[i] = p.OID
	}
	return &snmpDriver{dev: d, conn: g, oids: oids}, nil
}

func (s *snmpDriver) Poll(ctx context.Context) ([]Reading, error) {
	if s.conn.Conn == nil {
		if err := s.conn.Connect(); err != nil {
			return nil, err
		}
	}
	res, err := s.conn.Get(s.oids)
	if err != nil {
		s.conn.Conn.Close()
		s.conn.Conn = nil // re-dial next poll
		return nil, err
	}
	byOID := map[string]gosnmp.SnmpPDU{}
	for _, v := range res.Variables {
		byOID[v.Name] = v
	}
	out := make([]Reading, 0, len(s.dev.Points))
	for _, p := range s.dev.Points {
		pdu, ok := byOID[p.OID]
		if !ok {
			return out, fmt.Errorf("point %s: no value for %s", p.ID, p.OID)
		}
		v, err := snmpNumber(pdu)
		if err != nil {
			return out, fmt.Errorf("point %s: %w", p.ID, err)
		}
		scale := p.Scale
		if scale == 0 {
			scale = 1
		}
		v *= scale
		if math.IsNaN(v) || v < p.Min || v > p.Max {
			return out, fmt.Errorf("point %s: value %v outside [%v,%v]", p.ID, v, p.Min, p.Max)
		}
		out = append(out, Reading{DeviceID: s.dev.ID, PointID: p.ID, Value: v, Unit: p.Unit})
	}
	return out, nil
}

func snmpNumber(p gosnmp.SnmpPDU) (float64, error) {
	switch p.Type {
	case gosnmp.Integer, gosnmp.Counter32, gosnmp.Gauge32, gosnmp.TimeTicks, gosnmp.Counter64, gosnmp.Uinteger32:
		return float64(gosnmp.ToBigInt(p.Value).Int64()), nil
	case gosnmp.OpaqueFloat:
		if f, ok := p.Value.(float32); ok {
			return float64(f), nil
		}
	case gosnmp.OpaqueDouble:
		if f, ok := p.Value.(float64); ok {
			return f, nil
		}
	case gosnmp.OctetString:
		// Some agents return numbers as text ("23.4").
		b, _ := p.Value.([]byte)
		if f, err := strconv.ParseFloat(strings.TrimSpace(string(b)), 64); err == nil {
			return f, nil
		}
	case gosnmp.NoSuchObject, gosnmp.NoSuchInstance, gosnmp.EndOfMibView:
		return 0, fmt.Errorf("agent has no such object")
	}
	return 0, fmt.Errorf("non-numeric SNMP type %v", p.Type)
}

func (s *snmpDriver) Close() error {
	if s.conn.Conn != nil {
		return s.conn.Conn.Close()
	}
	return nil
}
