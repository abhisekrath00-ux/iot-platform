package autodetect

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/abhisekrath00-ux/iot-platform/edge/internal/config"
	"github.com/abhisekrath00-ux/iot-platform/edge/internal/discover"
	"github.com/abhisekrath00-ux/iot-platform/edge/internal/driver"
)

// Payload has the same shape as a dashboard scan result (scan/result topic), plus
// params, so the server can show it on the Scan page and the one-click Add works
// unchanged. scan_id starts with "auto-": the ingest service accepts such results
// without a prior request, as proposals only.
type Payload struct {
	ScanID     string                  `json:"scan_id"`
	Kind       string                  `json:"kind"`
	OK         bool                    `json:"ok"`
	Auto       bool                    `json:"auto"`
	Params     map[string]any          `json:"params"`
	Slaves     []driver.Slave          `json:"slaves,omitempty"`
	Hosts      []discover.Hit          `json:"hosts,omitempty"`
	BACnet     []discover.BACnetDevice `json:"bacnet,omitempty"`
	FinishedAt time.Time               `json:"finished_at"`
	scope, sum string
}

func shortHash(parts []string) string {
	sort.Strings(parts)
	h := sha256.Sum256([]byte(strings.Join(parts, "\n")))
	return hex.EncodeToString(h[:])[:12]
}

// Payloads groups findings into results: one per serial port+settings, one for LAN, one for BACnet.
// sum is a hash of what is in the result, so the caller can skip a result the server already has.
func Payloads(fs []Finding, from, to int, now time.Time) []Payload {
	byScope := map[string]*Payload{}
	keys := map[string][]string{}
	get := func(scope, kind string, params map[string]any) *Payload {
		p := byScope[scope]
		if p == nil {
			p = &Payload{Kind: kind, OK: true, Auto: true, Params: params, FinishedAt: now.UTC(), scope: scope}
			byScope[scope] = p
		}
		return p
	}
	for _, f := range fs {
		switch f.Kind {
		case "modbus-rtu":
			sc := fmt.Sprintf("modbus-rtu:%s:%d%s", f.Port, f.Baud, f.Parity)
			p := get(sc, "modbus-rtu", map[string]any{"port": f.Port, "baud": f.Baud, "data_bits": 8, "stop_bits": 1, "parity": f.Parity, "from": from, "to": to})
			p.Slaves = append(p.Slaves, driver.Slave{Address: f.Address, Matches: f.Matches})
			m := ""
			if len(f.Matches) > 0 {
				m = f.Matches[0].ProfileID
			}
			keys[sc] = append(keys[sc], f.Key+"="+m)
		case "lan":
			p := get("lan", "lan", map[string]any{"cidr": "local subnets"})
			p.Hosts = append(p.Hosts, discover.Hit{Addr: f.Host, Port: f.NetPort, Service: f.Service})
			keys["lan"] = append(keys["lan"], f.Key)
		case "bacnet":
			p := get("bacnet", "bacnet", map[string]any{"broadcast": "local subnets"})
			p.BACnet = append(p.BACnet, discover.BACnetDevice{Addr: f.Host, Instance: f.Instance, Vendor: uint32(f.Vendor)})
			keys["bacnet"] = append(keys["bacnet"], f.Key)
		}
	}
	var out []Payload
	for sc, p := range byScope {
		p.sum = shortHash(keys[sc])
		p.ScanID = "auto-" + p.sum + "-" + fmt.Sprint(now.Unix())
		out = append(out, *p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].scope < out[j].scope })
	return out
}

func (p Payload) Scope() string { return p.scope }
func (p Payload) Sum() string   { return p.sum }

// AutoAdd writes confident Modbus RTU matches to the overlay file and returns the
// devices added. It never edits the operator's own config and never overwrites an
// existing device. A device is only added when its profile has a full polling
// definition on this gateway.
func AutoAdd(cfg *config.Config, profiles []Profile, fs []Finding, overlay string) ([]config.Device, error) {
	existing, err := config.LoadOverlay(overlay)
	if err != nil {
		return nil, err
	}
	taken := func(port string, addr int) bool {
		for _, d := range append(append([]config.Device{}, cfg.Devices...), existing...) {
			if d.Port != "" && samePort(d.Port, port) && d.Address == addr {
				return true
			}
		}
		return false
	}
	var added []config.Device
	for _, f := range fs {
		if f.Kind != "modbus-rtu" || !f.Confident || f.State != "suggested" || taken(f.Port, f.Address) {
			continue
		}
		var prof *Profile
		for i := range profiles {
			if profiles[i].ID == f.Profile {
				prof = &profiles[i]
			}
		}
		if prof == nil || len(prof.Points) == 0 {
			continue
		}
		short := strings.TrimPrefix(prof.ID, "builtin:")
		id := fmt.Sprintf("auto-%s-%s-%d", short, strings.ToLower(strings.Trim(strings.ReplaceAll(filepath.Base(strings.ReplaceAll(f.Port, `\`, "/")), " ", ""), ".")), f.Address)
		d := config.Device{ID: id, Profile: "modbus-generic", Port: f.Port, Baud: f.Baud, DataBits: 8, StopBits: 1, Parity: f.Parity,
			Address: f.Address, Interval: prof.Interval, Points: prof.Points}
		existing = append(existing, d)
		added = append(added, d)
	}
	if len(added) == 0 {
		return nil, nil
	}
	return added, config.SaveOverlay(overlay, existing)
}
