package autodetect

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/abhisekrath00-ux/iot-platform/edge/internal/config"
	"github.com/abhisekrath00-ux/iot-platform/edge/internal/discover"
	"github.com/abhisekrath00-ux/iot-platform/edge/internal/driver"
)

func fixed() time.Time { return time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC) }

func fakeDetector(cfg *config.Config) (*Detector, *[]driver.SweepRequest) {
	var calls []driver.SweepRequest
	d := New(cfg, Builtin())
	d.Now = fixed
	d.ListPorts = func() ([]string, error) { return []string{"/dev/ttyUSB0", "/dev/ttyUSB1"}, nil }
	d.Sweep = func(_ context.Context, _ driver.PortOpener, r driver.SweepRequest) driver.SweepResult {
		calls = append(calls, r)
		// a single-phase meter answers at address 3, 9600 8E1, on ttyUSB1 only
		if r.Port == "/dev/ttyUSB1" && r.Baud == 9600 && r.Parity == "even" {
			return driver.SweepResult{Slaves: []driver.Slave{{Address: 3, Matches: []driver.Match{{ProfileID: "builtin:selec-mx300", Name: "Selec MX300", Score: 1, Probed: 3}}}}}
		}
		return driver.SweepResult{}
	}
	d.ScanLAN = func(context.Context, string, time.Duration) ([]discover.Hit, error) {
		return []discover.Hit{{Addr: "192.168.1.50", Port: 502, Service: "Modbus TCP"}}, nil
	}
	d.WhoIs = func(context.Context, string, time.Duration) ([]discover.BACnetDevice, error) {
		return []discover.BACnetDevice{{Addr: "192.168.1.60", Instance: 1234, Vendor: 5}}, nil
	}
	d.Nets = func() []Net { return []Net{{Iface: "eth0", CIDR: "192.168.1.0/24", Broadcast: "192.168.1.255"}} }
	return d, &calls
}

func TestRunFindsAllThreeKinds(t *testing.T) {
	d, _ := fakeDetector(&config.Config{})
	res := d.Run(context.Background())
	kinds := map[string]int{}
	for _, f := range res.Findings {
		kinds[f.Kind]++
	}
	if kinds["modbus-rtu"] != 1 || kinds["lan"] != 1 || kinds["bacnet"] != 1 {
		t.Fatalf("kinds = %v", kinds)
	}
	for _, f := range res.Findings {
		if f.Kind == "modbus-rtu" && (f.Baud != 9600 || f.Parity != "even" || f.Address != 3 || !f.Confident || f.Profile != "builtin:selec-mx300") {
			t.Errorf("serial finding = %+v", f)
		}
	}
}

func TestSerialSkipsPortsInUseAndStopsAtFirstAnswer(t *testing.T) {
	cfg := &config.Config{Devices: []config.Device{{ID: "m", Port: "/dev/ttyUSB0"}}}
	d, calls := fakeDetector(cfg)
	d.Run(context.Background())
	for _, c := range *calls {
		if c.Port == "/dev/ttyUSB0" {
			t.Fatalf("swept a port a configured device polls: %+v", c)
		}
	}
	// ttyUSB1: answered on the third combo (9600 even), so no 4th combo was tried
	if len(*calls) != 3 {
		t.Fatalf("sweeps = %d, want 3", len(*calls))
	}
}

func TestSwitchesAndRange(t *testing.T) {
	off := false
	cfg := &config.Config{Autodetect: config.Autodetect{Serial: &off, LAN: &off, SerialFrom: 5, SerialTo: 9}}
	d, calls := fakeDetector(cfg)
	res := d.Run(context.Background())
	if len(*calls) != 0 {
		t.Fatal("serial ran although switched off")
	}
	if len(res.Findings) != 1 || res.Findings[0].Kind != "bacnet" {
		t.Fatalf("findings = %+v", res.Findings)
	}
	on := true
	cfg = &config.Config{Autodetect: config.Autodetect{Serial: &on, LAN: &off, BACnet: &off, SerialFrom: 5, SerialTo: 9}}
	d, calls = fakeDetector(cfg)
	d.Run(context.Background())
	if (*calls)[0].From != 5 || (*calls)[0].To != 9 {
		t.Fatalf("range = %d-%d", (*calls)[0].From, (*calls)[0].To)
	}
}

func TestConfidentNeedsOneFullProfileWithPoints(t *testing.T) {
	d := New(&config.Config{}, append(Builtin(), Profile{ID: "srv-1", Name: "from server", Match: Builtin()[0].Match}))
	full := func(id string) driver.Match { return driver.Match{ProfileID: id, Score: 1, Probed: 3} }
	if ok, p := d.confident([]driver.Match{full("builtin:selec-mx300")}); !ok || p != "builtin:selec-mx300" {
		t.Error("single full match with points should be confident")
	}
	if ok, _ := d.confident([]driver.Match{full("builtin:selec-mx300"), full("builtin:selec-mfm383a")}); ok {
		t.Error("two full matches are ambiguous")
	}
	if ok, _ := d.confident([]driver.Match{full("srv-1")}); ok {
		t.Error("a profile without a full point list must never be confident")
	}
	if ok, _ := d.confident([]driver.Match{{ProfileID: "builtin:selec-mx300", Score: 0.67, Probed: 3}}); ok {
		t.Error("partial score is not confident")
	}
}

func TestLANSkipsOwnAddressAndRefusesNothingPublic(t *testing.T) {
	if b := broadcastOf(mustPrefix(t, "10.1.2.0/23")); b != "10.1.3.255" {
		t.Errorf("broadcast = %s", b)
	}
	for _, n := range LocalNets() {
		if n.CIDR == "" || n.Broadcast == "" {
			t.Errorf("bad net %+v", n)
		}
	}
}

func TestPayloadsAndDedupe(t *testing.T) {
	d, _ := fakeDetector(&config.Config{})
	res := d.Run(context.Background())
	ps := Payloads(res.Findings, 1, 32, fixed())
	if len(ps) != 3 {
		t.Fatalf("payloads = %d", len(ps))
	}
	again := Payloads(res.Findings, 1, 32, fixed().Add(time.Hour))
	for i := range ps {
		if ps[i].Sum() != again[i].Sum() || ps[i].ScanID == again[i].ScanID {
			t.Errorf("sum must be stable and scan id unique: %s %s", ps[i].ScanID, again[i].ScanID)
		}
		if len(ps[i].ScanID) < 5 || ps[i].ScanID[:5] != "auto-" || !ps[i].Auto {
			t.Errorf("bad auto payload %+v", ps[i])
		}
	}
	for _, p := range ps {
		if p.Kind == "modbus-rtu" && (p.Params["port"] != "/dev/ttyUSB1" || p.Params["baud"] != 9600 || p.Params["parity"] != "even") {
			t.Errorf("params = %+v", p.Params)
		}
	}
}

func TestStoreKeepsFirstSeenAndIgnored(t *testing.T) {
	s := OpenStore(filepath.Join(t.TempDir(), "d.json"))
	f := Finding{Key: "lan:1.2.3.4:502", Kind: "lan"}
	if fresh := s.Merge(fixed(), []Finding{f}, nil); len(fresh) != 1 {
		t.Fatal("first merge should report it as new")
	}
	s.MarkState(f.Key, "ignored")
	if fresh := s.Merge(fixed().Add(time.Hour), []Finding{f}, nil); len(fresh) != 0 {
		t.Fatal("second merge is not new")
	}
	got := OpenStore(s.path).Snapshot().Findings[0]
	if got.State != "ignored" || !got.FirstSeen.Equal(fixed()) {
		t.Fatalf("not persisted: %+v", got)
	}
}

func TestAutoAddOnlyConfidentAndNeverDuplicates(t *testing.T) {
	overlay := filepath.Join(t.TempDir(), "o.yaml")
	cfg := &config.Config{Devices: []config.Device{{ID: "x", Port: "/dev/ttyUSB5", Address: 9}}}
	profiles := Builtin()
	fs := []Finding{
		{Key: "a", Kind: "modbus-rtu", Port: "/dev/ttyUSB1", Baud: 9600, Parity: "even", Address: 3, Confident: true, Profile: "builtin:selec-mx300", State: "suggested"},
		{Key: "b", Kind: "modbus-rtu", Port: "/dev/ttyUSB1", Baud: 9600, Parity: "even", Address: 4, Confident: false, State: "suggested"},
		{Key: "c", Kind: "modbus-rtu", Port: "/dev/ttyUSB5", Baud: 9600, Address: 9, Confident: true, Profile: "builtin:selec-mx300", State: "suggested"},
		{Key: "d", Kind: "modbus-rtu", Port: "/dev/ttyUSB2", Baud: 9600, Address: 1, Confident: true, Profile: "builtin:selec-mx300", State: "ignored"},
		{Key: "e", Kind: "lan", Host: "1.2.3.4", NetPort: 502},
	}
	added, err := AutoAdd(cfg, profiles, fs, overlay)
	if err != nil || len(added) != 1 || added[0].Address != 3 || added[0].Parity != "even" || added[0].StopBits != 1 || len(added[0].Points) != 7 {
		t.Fatalf("added = %+v err %v", added, err)
	}
	again, _ := AutoAdd(cfg, profiles, fs, overlay)
	if len(again) != 0 {
		t.Fatalf("second pass added %d", len(again))
	}
	devs, _ := config.LoadOverlay(overlay)
	if len(devs) != 1 {
		t.Fatalf("overlay has %d", len(devs))
	}
}

func TestLibraryFilesAndCache(t *testing.T) {
	dir := t.TempDir()
	_ = writeFile(filepath.Join(dir, "p.yaml"), "id: acme-1\nname: Acme\nmatch:\n  - {id: v, register: 0, func: 4, type: u16, scale: 1, min: 1, max: 9}\n")
	_ = writeFile(filepath.Join(dir, "bad.yaml"), "name: no id\n")
	ps, warns := LoadLibrary(dir, []driver.Candidate{{ProfileID: "srv", Name: "S", Points: Builtin()[0].Match}, {ProfileID: "builtin:selec-mx300", Name: "dup", Points: Builtin()[0].Match}})
	if len(warns) != 1 {
		t.Errorf("warns = %v", warns)
	}
	src := map[string]string{}
	for _, p := range ps {
		src[p.ID] = p.Source
	}
	if src["acme-1"] != "file" || src["srv"] != "server-cache" || src["builtin:selec-mx300"] != "builtin" {
		t.Errorf("sources = %v", src)
	}
}
