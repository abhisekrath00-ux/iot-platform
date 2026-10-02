package autodetect

import (
	"context"
	"fmt"
	"net"
	"net/netip"
	"sort"
	"strings"
	"time"

	"go.bug.st/serial"

	"github.com/abhisekrath00-ux/iot-platform/edge/internal/config"
	"github.com/abhisekrath00-ux/iot-platform/edge/internal/discover"
	"github.com/abhisekrath00-ux/iot-platform/edge/internal/driver"
)

// Everything here only reads: Modbus read functions, TCP connects and a BACnet
// Who-Is. The one thing that is not free is serial: a sweep talks on a shared
// RS-485 bus, which is why ports configured for polling are skipped.

// Combo is one set of serial settings to try.
type Combo struct {
	Baud   int
	Parity string
}

// Combos are tried in order of how common they are on meters; the sweep of a port
// stops at the first one where a slave answers.
var Combos = []Combo{
	{9600, "none"}, {19200, "none"}, {9600, "even"}, {19200, "even"},
	{38400, "none"}, {4800, "none"}, {9600, "odd"}, {115200, "none"},
}

// Detector holds the inputs and the replaceable system calls (so tests need no hardware).
type Detector struct {
	Cfg      *config.Config
	Profiles []Profile
	Open     driver.PortOpener

	ListPorts func() ([]string, error)
	Sweep     func(context.Context, driver.PortOpener, driver.SweepRequest) driver.SweepResult
	ScanLAN   func(context.Context, string, time.Duration) ([]discover.Hit, error)
	WhoIs     func(context.Context, string, time.Duration) ([]discover.BACnetDevice, error)
	Nets      func() []Net
	Now       func() time.Time
}

// Net is one local private IPv4 network.
type Net struct {
	Iface     string
	CIDR      string // what to scan (never larger than a /24)
	Broadcast string
}

func New(cfg *config.Config, profiles []Profile) *Detector {
	return &Detector{
		Cfg: cfg, Profiles: profiles,
		ListPorts: serial.GetPortsList, Sweep: driver.SweepModbusRTU,
		ScanLAN: discover.ScanLAN, WhoIs: discover.FindBACnet,
		Nets: LocalNets, Now: time.Now,
	}
}

// Result of one run.
type Result struct {
	Findings []Finding
	Notes    []string
}

func samePort(a, b string) bool {
	return strings.EqualFold(strings.TrimPrefix(a, `\\.\`), strings.TrimPrefix(b, `\\.\`))
}

func (d *Detector) busy(port string) bool {
	for _, dev := range d.Cfg.Devices {
		if dev.Port != "" && samePort(dev.Port, port) {
			return true
		}
	}
	return false
}

// Run does one discovery pass over serial, LAN and BACnet, per config.
func (d *Detector) Run(ctx context.Context) Result {
	var res Result
	a := d.Cfg.Autodetect
	if a.DoSerial() {
		f, n := d.serialPass(ctx)
		res.Findings, res.Notes = append(res.Findings, f...), append(res.Notes, n...)
	}
	nets := d.nets()
	if a.DoLAN() {
		f, n := d.lanPass(ctx, nets)
		res.Findings, res.Notes = append(res.Findings, f...), append(res.Notes, n...)
	}
	if a.DoBACnet() {
		f, n := d.bacnetPass(ctx, nets)
		res.Findings, res.Notes = append(res.Findings, f...), append(res.Notes, n...)
	}
	return res
}

func (d *Detector) serialPass(ctx context.Context) ([]Finding, []string) {
	var out []Finding
	var notes []string
	ports, err := d.ListPorts()
	if err != nil {
		return nil, []string{"serial: cannot list ports: " + err.Error()}
	}
	for _, extra := range d.Cfg.Autodetect.SerialPorts {
		dup := false
		for _, p := range ports {
			dup = dup || samePort(p, extra)
		}
		if !dup {
			ports = append(ports, extra)
		}
	}
	if len(ports) == 0 {
		return nil, []string{"serial: no serial ports on this machine"}
	}
	from, to := d.Cfg.Autodetect.Range()
	cands := Candidates(d.Profiles)
	now := d.Now().UTC()
	for _, port := range ports {
		if d.busy(port) {
			notes = append(notes, "serial: skipped "+port+" (a configured device polls it)")
			continue
		}
		found := false
		var lastErr string
		for _, c := range Combos {
			if ctx.Err() != nil {
				notes = append(notes, "serial: stopped early: "+ctx.Err().Error())
				return out, notes
			}
			r := d.Sweep(ctx, d.Open, driver.SweepRequest{Port: port, Baud: c.Baud, DataBits: 8, StopBits: 1, Parity: c.Parity,
				From: from, To: to, TimeoutMs: 250, Candidates: cands})
			if r.Error != "" {
				lastErr = r.Error
				break // cannot open this port at all; other settings will not help
			}
			for _, s := range r.Slaves {
				found = true
				f := Finding{Key: fmt.Sprintf("modbus-rtu:%s:%d%s:%d", port, c.Baud, c.Parity, s.Address), Kind: "modbus-rtu",
					Port: port, Baud: c.Baud, Parity: c.Parity, Address: s.Address, Matches: s.Matches, FirstSeen: now, LastSeen: now}
				f.Confident, f.Profile = d.confident(s.Matches)
				out = append(out, f)
			}
			if found {
				break
			}
		}
		switch {
		case lastErr != "":
			notes = append(notes, "serial: "+lastErr)
		case !found:
			notes = append(notes, fmt.Sprintf("serial: %s: no slave answered at addresses %d-%d with the %d common settings tried", port, from, to, len(Combos)))
		}
	}
	return out, notes
}

// confident means: one profile scored 1.0 on all three probed registers, it has a
// full polling definition on this gateway, and no other profile scored 1.0.
func (d *Detector) confident(ms []driver.Match) (bool, string) {
	var full []driver.Match
	for _, m := range ms {
		if m.Score >= 1 && m.Probed >= 3 {
			full = append(full, m)
		}
	}
	if len(full) != 1 {
		return false, ""
	}
	for _, p := range d.Profiles {
		if p.ID == full[0].ProfileID && len(p.Points) > 0 {
			return true, p.ID
		}
	}
	return false, ""
}

func (d *Detector) nets() []Net {
	if len(d.Cfg.Autodetect.LANCIDRs) > 0 {
		var out []Net
		for _, c := range d.Cfg.Autodetect.LANCIDRs {
			if p, err := netip.ParsePrefix(c); err == nil && p.Addr().Is4() {
				out = append(out, Net{Iface: "configured", CIDR: c, Broadcast: broadcastOf(p)})
			}
		}
		return out
	}
	return d.Nets()
}

func (d *Detector) lanPass(ctx context.Context, nets []Net) ([]Finding, []string) {
	var out []Finding
	var notes []string
	if len(nets) == 0 {
		return nil, []string{"lan: no private IPv4 network found on this machine"}
	}
	self := localAddrs()
	now := d.Now().UTC()
	for _, n := range nets {
		hits, err := d.ScanLAN(ctx, n.CIDR, 400*time.Millisecond)
		if err != nil {
			notes = append(notes, "lan: "+n.CIDR+": "+err.Error())
			continue
		}
		for _, h := range hits {
			if self[h.Addr] {
				continue // this gateway itself
			}
			out = append(out, Finding{Key: fmt.Sprintf("lan:%s:%d", h.Addr, h.Port), Kind: "lan", Host: h.Addr, NetPort: h.Port, Service: h.Service, FirstSeen: now, LastSeen: now})
		}
	}
	return out, notes
}

func (d *Detector) bacnetPass(ctx context.Context, nets []Net) ([]Finding, []string) {
	var out []Finding
	var notes []string
	seen := map[string]bool{}
	targets := []string{"255.255.255.255"}
	for _, n := range nets {
		targets = append(targets, n.Broadcast)
	}
	now := d.Now().UTC()
	for _, b := range targets {
		ds, err := d.WhoIs(ctx, b, 3*time.Second)
		if err != nil {
			notes = append(notes, "bacnet: "+b+": "+err.Error())
			continue
		}
		for _, x := range ds {
			k := fmt.Sprintf("bacnet:%s:%d", x.Addr, x.Instance)
			if seen[k] {
				continue
			}
			seen[k] = true
			out = append(out, Finding{Key: k, Kind: "bacnet", Host: x.Addr, Instance: x.Instance, Vendor: int(x.Vendor), FirstSeen: now, LastSeen: now})
		}
	}
	return out, notes
}

// LocalNets lists this machine's private IPv4 networks, skipping loopback,
// link-local and container bridges. A network wider than a /24 is narrowed to the
// /24 around this machine's own address so a scan stays small.
func LocalNets() []Net {
	var out []Net
	ifs, _ := net.Interfaces()
	for _, ifc := range ifs {
		if ifc.Flags&net.FlagUp == 0 || ifc.Flags&net.FlagLoopback != 0 {
			continue
		}
		name := strings.ToLower(ifc.Name)
		if strings.HasPrefix(name, "docker") || strings.HasPrefix(name, "br-") || strings.HasPrefix(name, "veth") || strings.HasPrefix(name, "virbr") {
			continue
		}
		addrs, _ := ifc.Addrs()
		for _, a := range addrs {
			ipn, ok := a.(*net.IPNet)
			if !ok {
				continue
			}
			ip, ok := netip.AddrFromSlice(ipn.IP.To4())
			if !ok || !ip.IsPrivate() {
				continue
			}
			ones, _ := ipn.Mask.Size()
			if ones < 24 {
				ones = 24
			}
			p := netip.PrefixFrom(ip, ones).Masked()
			out = append(out, Net{Iface: ifc.Name, CIDR: p.String(), Broadcast: broadcastOf(p)})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CIDR < out[j].CIDR })
	return out
}

func broadcastOf(p netip.Prefix) string {
	b := p.Masked().Addr().As4()
	host := 32 - p.Bits()
	v := uint32(b[0])<<24 | uint32(b[1])<<16 | uint32(b[2])<<8 | uint32(b[3])
	v |= (1 << uint(host)) - 1
	return fmt.Sprintf("%d.%d.%d.%d", byte(v>>24), byte(v>>16), byte(v>>8), byte(v))
}

func localAddrs() map[string]bool {
	m := map[string]bool{}
	as, _ := net.InterfaceAddrs()
	for _, a := range as {
		if ipn, ok := a.(*net.IPNet); ok {
			m[ipn.IP.String()] = true
		}
	}
	return m
}
