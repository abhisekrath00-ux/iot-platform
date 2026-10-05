package diagnose

import (
	"strings"
	"testing"
	"time"
)

var now = time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC)

func at(m int) *time.Time { t := now.Add(-time.Duration(m) * time.Minute); return &t }

func dev(id, gw, health string, seen *time.Time) Device {
	return Device{ID: id, Name: "dev-" + id, GatewayID: gw, Health: health, Score: map[string]int{"healthy": 95, "degraded": 60, "critical": 30, "offline": 5}[health], LastSeen: seen}
}

func TestSilentGatewayExplainsItsDevicesOnce(t *testing.T) {
	r := Run(Input{Scope: "north", Now: now,
		Gateways: []Gateway{{ID: "g1", Serial: "GW-N1", Status: "active", LastSeen: at(95)}, {ID: "g2", Serial: "GW-S1", Status: "active", LastSeen: at(1)}},
		Devices:  []Device{dev("a", "g1", "offline", at(95)), dev("b", "g1", "offline", at(95)), dev("c", "g2", "healthy", at(1))}})
	if r.Severity != "critical" || len(r.Findings) != 1 {
		t.Fatalf("one finding expected, devices behind a silent gateway must not be reported twice: %+v", r.Findings)
	}
	f := r.Findings[0]
	if !strings.Contains(f.Problem, "GW-N1") || !strings.Contains(f.Evidence[0], "95 min ago") || !strings.Contains(f.Evidence[1], "2 of 2") {
		t.Fatalf("%+v", f)
	}
	if f.PossibleFix.Policy != AutoNever || !strings.Contains(f.LikelyCause, "Likely") {
		t.Fatalf("a gateway outage has no automatic fix and its cause is only 'likely': %+v", f)
	}
}

func TestDeviceOutageBehindHealthyGateway(t *testing.T) {
	r := Run(Input{Now: now, Gateways: []Gateway{{ID: "g1", Serial: "GW", Status: "active", LastSeen: at(1)}},
		Devices: []Device{dev("a", "g1", "offline", at(300)), dev("b", "g1", "healthy", at(1)), dev("c", "g1", "healthy", at(1))}})
	if len(r.Findings) != 1 || !strings.Contains(r.Findings[0].Problem, "1 device") || r.Findings[0].Severity != "warning" {
		t.Fatalf("%+v", r.Findings)
	}
	if !strings.Contains(r.Findings[0].Evidence[0], "5 h ago") {
		t.Fatalf("%v", r.Findings[0].Evidence)
	}
}

func TestAlertsRankedAndAckIsApprovalRequired(t *testing.T) {
	r := Run(Input{Now: now, Gateways: []Gateway{{ID: "g", Serial: "GW", Status: "active", LastSeen: at(1)}},
		Devices: []Device{dev("a", "g", "healthy", at(1))},
		Alerts:  []Alert{{ID: "1", Severity: "warning", Message: "Fan slow", At: now.Add(-time.Hour)}, {ID: "2", Severity: "critical", Message: "Tank low", At: now.Add(-time.Minute)}}})
	if r.Severity != "critical" || len(r.Findings) != 1 {
		t.Fatalf("%+v", r.Findings)
	}
	f := r.Findings[0]
	if !strings.Contains(f.Evidence[1], "Tank low") || f.PossibleFix.Policy != ApprovalRequired || f.PossibleFix.Tool != "acknowledge_alert" {
		t.Fatalf("%+v", f)
	}
	if strings.Contains(f.LikelyCause, "outage") {
		t.Fatalf("with no outage the cause must not mention one: %s", f.LikelyCause)
	}
}

func TestAlertsDuringOutageAreCorrelationNotCause(t *testing.T) {
	r := Run(Input{Now: now, Gateways: []Gateway{{ID: "g", Serial: "GW", Status: "offline", LastSeen: at(500)}},
		Devices: []Device{dev("a", "g", "offline", at(500))}, Alerts: []Alert{{ID: "1", Severity: "critical", Message: "Stale", At: now}}})
	var alertF *Finding
	for i := range r.Findings {
		if strings.Contains(r.Findings[i].Problem, "alert") {
			alertF = &r.Findings[i]
		}
	}
	if alertF == nil || !strings.Contains(alertF.LikelyCause, "correlation, not proof") {
		t.Fatalf("%+v", r.Findings)
	}
}

func TestHealthyScopeSaysWhatWasAndWasNotExamined(t *testing.T) {
	r := Run(Input{Now: now, Gateways: []Gateway{{ID: "g", Serial: "GW", Status: "active", LastSeen: at(2)}}, Devices: []Device{dev("a", "g", "healthy", at(1))}})
	if r.Severity != "info" || len(r.Findings) != 1 || !strings.Contains(r.Findings[0].Problem, "No problem found") {
		t.Fatalf("%+v", r.Findings)
	}
	if len(r.NotExamined) == 0 || len(r.Examined) == 0 {
		t.Fatal("a clean result must still say what was not looked at")
	}
}

func TestDeterministic(t *testing.T) {
	in := Input{Now: now, Gateways: []Gateway{{ID: "g", Serial: "GW", Status: "active", LastSeen: at(1)}},
		Devices: []Device{dev("a", "g", "offline", at(60)), dev("b", "g", "degraded", at(3))}, Alerts: []Alert{{ID: "1", Severity: "warning", Message: "x", At: now}}}
	a, b := Run(in), Run(in)
	if len(a.Findings) != len(b.Findings) || a.Findings[0].Problem != b.Findings[0].Problem {
		t.Fatal("same input, different report")
	}
}

func TestNoGatewayMeansNoCrash(t *testing.T) {
	r := Run(Input{Now: now})
	if len(r.Findings) != 1 {
		t.Fatalf("%+v", r.Findings)
	}
}
