package driver

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/abhisekrath00-ux/iot-platform/edge/internal/config"
	"github.com/gosnmp/gosnmp"
)

// fakeSNMPAgent is a minimal v2c agent: it answers GET for the OIDs in vals and
// returns noSuchObject for anything else. It uses gosnmp's own codec.
func fakeSNMPAgent(t *testing.T, community string, vals map[string]gosnmp.SnmpPDU) int {
	t.Helper()
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { pc.Close() })
	dec := &gosnmp.GoSNMP{Version: gosnmp.Version2c, Community: community}
	go func() {
		buf := make([]byte, 65535)
		for {
			n, from, err := pc.ReadFrom(buf)
			if err != nil {
				return
			}
			req, err := dec.SnmpDecodePacket(buf[:n])
			if err != nil || req.Community != community {
				continue // real agents drop bad community silently
			}
			resp := &gosnmp.SnmpPacket{Version: gosnmp.Version2c, Community: community, PDUType: gosnmp.GetResponse, RequestID: req.RequestID}
			for _, v := range req.Variables {
				if pdu, ok := vals[v.Name]; ok {
					resp.Variables = append(resp.Variables, pdu)
				} else {
					resp.Variables = append(resp.Variables, gosnmp.SnmpPDU{Name: v.Name, Type: gosnmp.NoSuchObject})
				}
			}
			b, err := resp.MarshalMsg()
			if err == nil {
				pc.WriteTo(b, from)
			}
		}
	}()
	return pc.LocalAddr().(*net.UDPAddr).Port
}

func TestSNMPv2cPoll(t *testing.T) {
	t.Setenv("TEST_SNMP_COMMUNITY", "s3cret")
	port := fakeSNMPAgent(t, "s3cret", map[string]gosnmp.SnmpPDU{
		".1.3.6.1.2.1.33.1.2.7.0": {Name: ".1.3.6.1.2.1.33.1.2.7.0", Type: gosnmp.Integer, Value: 31},    // UPS battery temp
		".1.3.6.1.4.1.9.1.1":      {Name: ".1.3.6.1.4.1.9.1.1", Type: gosnmp.Gauge32, Value: uint(2305)}, // volts x10
		".1.3.6.1.4.1.9.1.2":      {Name: ".1.3.6.1.4.1.9.1.2", Type: gosnmp.OctetString, Value: []byte("12.5")},
	})
	d, err := New(config.Device{ID: "ups1", Profile: "snmp", Host: "127.0.0.1", NetPort: port, CommunityEnv: "TEST_SNMP_COMMUNITY",
		Points: []config.Point{
			{ID: "batt_temp", OID: ".1.3.6.1.2.1.33.1.2.7.0", Unit: "C", Min: 0, Max: 100},
			{ID: "volts", OID: ".1.3.6.1.4.1.9.1.1", Scale: 0.1, Unit: "V", Min: 0, Max: 500},
			{ID: "amps", OID: ".1.3.6.1.4.1.9.1.2", Unit: "A", Min: 0, Max: 100},
		}})
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	got, err := d.Poll(context.Background())
	if err != nil || len(got) != 3 {
		t.Fatalf("%v %v", err, got)
	}
	if got[0].Value != 31 || got[1].Value < 230.49 || got[1].Value > 230.51 || got[2].Value != 12.5 {
		t.Fatalf("values %+v", got)
	}
}

func TestSNMPRefusalsAndErrors(t *testing.T) {
	pts := []config.Point{{ID: "x", OID: ".1.3.6.1.2.1.1.3.0", Min: 0, Max: 1}}
	t.Setenv("C", "c")
	t.Setenv("A", "authpassword")
	t.Setenv("P", "privpassword")
	bad := map[string]config.Device{
		"no host":      {ID: "d", Profile: "snmp", CommunityEnv: "C", Points: pts},
		"no community": {ID: "d", Profile: "snmp", Host: "h", Points: pts},
		"unset env":    {ID: "d", Profile: "snmp", Host: "h", CommunityEnv: "NOPE_UNSET", Points: pts},
		"v3 md5":       {ID: "d", Profile: "snmp", Host: "h", SNMPVersion: "3", Username: "u", AuthProto: "md5", AuthPassEnv: "A", PrivProto: "aes", PrivPassEnv: "P", Points: pts},
		"v3 des":       {ID: "d", Profile: "snmp", Host: "h", SNMPVersion: "3", Username: "u", AuthProto: "sha", AuthPassEnv: "A", PrivProto: "des", PrivPassEnv: "P", Points: pts},
		"v3 no priv":   {ID: "d", Profile: "snmp", Host: "h", SNMPVersion: "3", Username: "u", AuthProto: "sha", AuthPassEnv: "A", Points: pts},
		"v3 no user":   {ID: "d", Profile: "snmp", Host: "h", SNMPVersion: "3", AuthProto: "sha", AuthPassEnv: "A", PrivProto: "aes", PrivPassEnv: "P", Points: pts},
		"name oid":     {ID: "d", Profile: "snmp", Host: "h", CommunityEnv: "C", Points: []config.Point{{ID: "x", OID: "sysUpTime.0", Min: 0, Max: 1}}},
		"bad version":  {ID: "d", Profile: "snmp", Host: "h", SNMPVersion: "1", CommunityEnv: "C", Points: pts},
	}
	for name, dev := range bad {
		if _, err := New(dev); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
	if _, err := New(config.Device{ID: "d", Profile: "snmp", Host: "h", SNMPVersion: "3", Username: "u", AuthProto: "sha256", AuthPassEnv: "A", PrivProto: "aes256", PrivPassEnv: "P", Points: pts}); err != nil {
		t.Errorf("valid v3 config refused: %v", err)
	}
	// wrong community: agent never answers, poll must time out as an error
	t.Setenv("WRONG", "wrong")
	port := fakeSNMPAgent(t, "right", nil)
	d, _ := New(config.Device{ID: "d", Profile: "snmp", Host: "127.0.0.1", NetPort: port, CommunityEnv: "WRONG", Points: pts})
	snmpd := d.(*snmpDriver)
	snmpd.conn.Timeout, snmpd.conn.Retries = 300*time.Millisecond, 0
	if _, err := d.Poll(context.Background()); err == nil {
		t.Fatal("wrong community returned data")
	}
	// out of range and missing object are errors, not silent values
	t.Setenv("RIGHT", "right")
	port = fakeSNMPAgent(t, "right", map[string]gosnmp.SnmpPDU{".1.3.6.1.2.1.1.3.0": {Name: ".1.3.6.1.2.1.1.3.0", Type: gosnmp.Integer, Value: 50}})
	d, _ = New(config.Device{ID: "d", Profile: "snmp", Host: "127.0.0.1", NetPort: port, CommunityEnv: "RIGHT", Points: pts})
	if _, err := d.Poll(context.Background()); err == nil {
		t.Fatal("out-of-range value accepted")
	}
	d, _ = New(config.Device{ID: "d", Profile: "snmp", Host: "127.0.0.1", NetPort: port, CommunityEnv: "RIGHT", Points: []config.Point{{ID: "y", OID: ".1.3.6.1.9.9.9", Min: 0, Max: 1}}})
	if _, err := d.Poll(context.Background()); err == nil {
		t.Fatal("missing object accepted")
	}
}
