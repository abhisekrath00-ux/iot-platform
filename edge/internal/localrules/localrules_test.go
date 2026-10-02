package localrules

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/abhisekrath00-ux/iot-platform/edge/internal/config"
)

type clock struct{ t time.Time }

func (c *clock) now() time.Time      { return c.t }
func (c *clock) adv(d time.Duration) { c.t = c.t.Add(d) }

type siren struct {
	on    bool
	calls []bool
	fail  bool
}

func (s *siren) set(on bool) error {
	if s.fail {
		return os.ErrPermission
	}
	s.on = on
	s.calls = append(s.calls, on)
	return nil
}

func setup(t *testing.T, rules []config.Rule, link *bool) (*Engine, *siren, *clock) {
	s, c := &siren{}, &clock{time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)}
	outs := []config.Output{{Name: "siren", Class: "alarm", Kind: "simulate", MaxOn: 30 * time.Second}}
	e, err := New(outs, rules, map[string]Setter{"siren": s.set}, func() bool { return *link }, c.now)
	if err != nil {
		t.Fatal(err)
	}
	return e, s, c
}

func TestLinkDownSoundsAfterHoldAndStopsWhenLinkReturns(t *testing.T) {
	up := true
	e, s, c := setup(t, []config.Rule{{ID: "r1", Type: "link_down", For: 20 * time.Second, Output: "siren"}}, &up)
	e.Tick()
	if s.on {
		t.Fatal("sounding with link up")
	}
	up = false
	for i := 0; i < 19; i++ {
		c.adv(time.Second)
		e.Tick()
	}
	if s.on {
		t.Fatal("sounded before hold time")
	}
	c.adv(time.Second)
	e.Tick()
	if s.on {
		t.Fatal("sounded at 19s")
	}
	c.adv(time.Second)
	e.Tick()
	if !s.on {
		t.Fatal("did not sound after 20s offline")
	}
	up = true
	c.adv(time.Second)
	e.Tick()
	if s.on {
		t.Fatal("still sounding after link returned")
	}
}

func TestMaxOnCapsSirenUntilAlarmClears(t *testing.T) {
	up := false
	e, s, c := setup(t, []config.Rule{{ID: "r1", Type: "link_down", Output: "siren"}}, &up)
	e.Tick()
	if !s.on {
		t.Fatal("no alarm")
	}
	c.adv(31 * time.Second)
	e.Tick()
	if s.on {
		t.Fatal("siren exceeded max_on")
	}
	c.adv(time.Minute)
	e.Tick()
	if s.on {
		t.Fatal("siren restarted without the alarm clearing")
	}
	up = true
	e.Tick()
	up = false
	e.Tick()
	if !s.on {
		t.Fatal("did not re-trigger after clear")
	}
}

func TestThresholdSilenceAndTest(t *testing.T) {
	up := true
	e, s, c := setup(t, []config.Rule{{ID: "hot", Type: "threshold", Device: "boiler", Point: "temp", Op: ">", Value: 90, Output: "siren"}}, &up)
	e.Observe("boiler", map[string]float64{"temp": 85})
	e.Tick()
	if s.on {
		t.Fatal("below threshold")
	}
	e.Observe("boiler", map[string]float64{"temp": 95})
	e.Tick()
	if !s.on {
		t.Fatal("threshold not fired")
	}
	if err := e.Silence("siren", 10*time.Second); err != nil {
		t.Fatal(err)
	}
	e.Tick()
	if s.on {
		t.Fatal("silence ignored")
	}
	c.adv(11 * time.Second)
	e.Tick()
	if !s.on {
		t.Fatal("did not resume after silence while still active")
	}
	e.Observe("boiler", map[string]float64{"temp": 80})
	e.Tick()
	if s.on {
		t.Fatal("did not clear")
	}
	if err := e.Test("siren", 3*time.Second); err != nil {
		t.Fatal(err)
	}
	e.Tick()
	if !s.on {
		t.Fatal("test did not sound")
	}
	c.adv(4 * time.Second)
	e.Tick()
	if s.on {
		t.Fatal("test did not end")
	}
	if e.Silence("nope", time.Second) == nil || e.Silence("siren", 48*time.Hour) == nil {
		t.Fatal("bad silence accepted")
	}
}

func TestStaleDeviceAndPulse(t *testing.T) {
	up := true
	e, s, c := setup(t, []config.Rule{{ID: "dead", Type: "stale", Device: "m1", For: 10 * time.Second, Output: "siren", Pattern: "pulse"}}, &up)
	e.Observe("m1", map[string]float64{"v": 1})
	c.adv(5 * time.Second)
	e.Tick()
	if s.on {
		t.Fatal("fresh device alarmed")
	}
	c.adv(6 * time.Second)
	e.Tick()
	first := s.on
	c.adv(time.Second)
	e.Tick()
	if first == s.on {
		t.Fatalf("pulse did not toggle (%v then %v)", first, s.on)
	}
	e.Observe("m1", map[string]float64{"v": 1})
	e.Tick()
	if s.on {
		t.Fatal("did not clear when data resumed")
	}
}

func TestSetterFailureIsRetriedAndAllOffOnShutdown(t *testing.T) {
	up := false
	e, s, _ := setup(t, []config.Rule{{ID: "r", Type: "link_down", Output: "siren"}}, &up)
	s.fail = true
	e.Tick()
	if _, outs := e.Status(); outs[0].Error == "" {
		t.Fatal("failure not reported")
	}
	s.fail = false
	e.Tick()
	if !s.on {
		t.Fatal("not retried")
	}
	e.AllOff()
	if s.on {
		t.Fatal("AllOff left the siren on")
	}
}

func TestValidate(t *testing.T) {
	good := []config.Output{{Name: "siren", Class: "alarm", Kind: "gpio_file", Path: "/sys/class/gpio/gpio17/value"}}
	cases := map[string]struct {
		outs  []config.Output
		rules []config.Rule
		want  string
	}{
		"non-alarm class":  {[]config.Output{{Name: "pump", Class: "process", Kind: "simulate"}}, nil, "class"},
		"unknown output":   {good, []config.Rule{{ID: "r", Type: "link_down", Output: "pump"}}, "allowlist"},
		"dup rule":         {good, []config.Rule{{ID: "r", Type: "link_down", Output: "siren"}, {ID: "r", Type: "link_down", Output: "siren"}}, "duplicate"},
		"bad op":           {good, []config.Rule{{ID: "r", Type: "threshold", Device: "d", Point: "p", Op: "=~", Output: "siren"}}, "op must"},
		"short stale":      {good, []config.Rule{{ID: "r", Type: "stale", Device: "d", For: time.Second, Output: "siren"}}, "stale needs"},
		"relative gpio":    {[]config.Output{{Name: "s", Class: "alarm", Kind: "gpio_file", Path: "gpio17"}}, nil, "absolute"},
		"huge max_on":      {[]config.Output{{Name: "s", Class: "alarm", Kind: "simulate", MaxOn: 5 * time.Hour}}, nil, "max_on"},
		"unknown kind":     {[]config.Output{{Name: "s", Class: "alarm", Kind: "exec"}}, nil, "kind"},
		"coil without dev": {[]config.Output{{Name: "s", Class: "alarm", Kind: "modbus_coil"}}, nil, "device and point"},
	}
	for name, c := range cases {
		errs := Validate(c.outs, c.rules)
		if len(errs) == 0 || !strings.Contains(errs[0].Error(), c.want) {
			t.Errorf("%s: %v", name, errs)
		}
	}
	if errs := Validate(good, []config.Rule{{ID: "ok", Type: "link_down", For: 30 * time.Second, Output: "siren"}}); len(errs) != 0 {
		t.Errorf("valid config rejected: %v", errs)
	}
}

func TestControlFileSilenceAndRejectsGarbage(t *testing.T) {
	up := false
	e, s, _ := setup(t, []config.Rule{{ID: "r", Type: "link_down", Output: "siren"}}, &up)
	p := filepath.Join(t.TempDir(), "ctl.json")
	if err := WriteControl(p, "silence", "siren", time.Minute); err != nil {
		t.Fatal(err)
	}
	e.consumeControl(p)
	e.Tick()
	if s.on {
		t.Fatal("control silence ignored")
	}
	if _, err := os.Stat(p); err == nil {
		t.Fatal("control file not consumed")
	}
	os.WriteFile(p, []byte("{not json"), 0o600)
	e.consumeControl(p)
	WriteControl(p, "reboot", "siren", time.Second)
	e.consumeControl(p) // unknown action: logged, ignored
}
