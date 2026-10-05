// Package diagnose is the deterministic half of the assistant's investigations. Given the facts
// about a scope (devices with their health, their gateways, open alerts) it applies fixed rules and
// returns findings in a fixed shape: problem, evidence, likely cause, severity, recommended action
// and possible fix. No model is involved and nothing here changes anything; the model only reads
// the result aloud. Every claim in a finding is built from the numbers passed in, and every cause
// is labelled "likely".
package diagnose

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

type Device struct {
	ID, Name, GatewayID string
	Health              string // healthy | degraded | critical | offline
	Score               int
	LastSeen            *time.Time
}

type Gateway struct {
	ID, Serial, Status string
	LastSeen           *time.Time
}

type Alert struct {
	ID, DeviceID, Severity, Message string
	At                              time.Time
}

type Input struct {
	Scope    string
	Now      time.Time
	Devices  []Device
	Gateways []Gateway
	Alerts   []Alert // open alerts only
}

// Fix policies, as the platform's auto-fix policy names them. Nothing here is ever AUTO: the
// platform has no restart or remote-repair capability, and acknowledging is a person's call.
const (
	AutoNever        = "NEVER_AUTO"
	ApprovalRequired = "APPROVAL_REQUIRED"
)

type Fix struct {
	Action string `json:"action"`
	Policy string `json:"policy"`
	Tool   string `json:"tool,omitempty"`
}

type Finding struct {
	Problem     string   `json:"problem"`
	Evidence    []string `json:"evidence"`
	LikelyCause string   `json:"likely_cause"`
	Severity    string   `json:"severity"` // critical | warning | info
	Recommended string   `json:"recommended_action"`
	PossibleFix Fix      `json:"possible_fix"`
}

type Report struct {
	Scope       string    `json:"scope"`
	Severity    string    `json:"severity"`
	Counts      Counts    `json:"counts"`
	Findings    []Finding `json:"findings"`
	Examined    []string  `json:"examined"`
	NotExamined []string  `json:"not_examined"`
}

type Counts struct {
	Devices    int `json:"devices"`
	Healthy    int `json:"healthy"`
	Degraded   int `json:"degraded"`
	Critical   int `json:"critical"`
	Offline    int `json:"offline"`
	Gateways   int `json:"gateways"`
	OpenAlerts int `json:"open_alerts"`
}

var rank = map[string]int{"info": 0, "warning": 1, "critical": 2}

const silentAfter = 10 * time.Minute

func ago(now time.Time, t *time.Time) string {
	if t == nil {
		return "never"
	}
	d := now.Sub(*t).Round(time.Minute)
	switch {
	case d < time.Minute:
		return "under a minute ago"
	case d < 3*time.Hour:
		return fmt.Sprintf("%d min ago", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("%.0f h ago", d.Hours())
	}
	return fmt.Sprintf("%.0f days ago", d.Hours()/24)
}

func names(ds []Device, max int) string {
	var n []string
	for i, d := range ds {
		if i == max {
			n = append(n, fmt.Sprintf("and %d more", len(ds)-max))
			break
		}
		n = append(n, d.Name)
	}
	return strings.Join(n, ", ")
}

// Run applies the rules. The same input always gives the same report.
func Run(in Input) Report {
	r := Report{Scope: in.Scope, Severity: "info", Findings: []Finding{}}
	r.Counts = Counts{Devices: len(in.Devices), Gateways: len(in.Gateways), OpenAlerts: len(in.Alerts)}
	byGW := map[string][]Device{}
	for _, d := range in.Devices {
		byGW[d.GatewayID] = append(byGW[d.GatewayID], d)
		switch d.Health {
		case "healthy":
			r.Counts.Healthy++
		case "degraded":
			r.Counts.Degraded++
		case "critical":
			r.Counts.Critical++
		default:
			r.Counts.Offline++
		}
	}
	down := func(d Device) bool { return d.Health == "offline" || d.Health == "critical" }
	explained := map[string]bool{} // devices already blamed on their gateway

	// 1. a silent gateway explains the devices behind it
	for _, g := range in.Gateways {
		ds := byGW[g.ID]
		silent := g.Status != "active" || g.LastSeen == nil || in.Now.Sub(*g.LastSeen) > silentAfter
		if !silent || len(ds) == 0 {
			continue
		}
		nd := 0
		for _, d := range ds {
			if down(d) {
				nd++
			}
			explained[d.ID] = true
		}
		sev := "warning"
		if nd == len(ds) {
			sev = "critical"
		}
		ev := []string{fmt.Sprintf("Gateway %s status %q, last heard from %s.", g.Serial, g.Status, ago(in.Now, g.LastSeen)),
			fmt.Sprintf("%d of %d devices behind it are offline or critical (%s).", nd, len(ds), names(ds, 5))}
		r.Findings = append(r.Findings, Finding{
			Problem:     fmt.Sprintf("Gateway %s is silent", g.Serial),
			Evidence:    ev,
			LikelyCause: "Likely the gateway or its network link: when every device behind one gateway stops at the same time, the cause is usually upstream of the devices (power, network, broker connection) rather than the devices themselves.",
			Severity:    sev,
			Recommended: "Check the gateway's power and network connection on site, then watch whether its devices report again.",
			PossibleFix: Fix{Action: "No remote repair exists in the platform; a person must check the gateway.", Policy: AutoNever},
		})
	}

	// 2. devices down while their gateway is alive
	var devDown []Device
	for _, d := range in.Devices {
		if down(d) && !explained[d.ID] {
			devDown = append(devDown, d)
		}
	}
	if len(devDown) > 0 {
		var ev []string
		for i, d := range devDown {
			if i == 5 {
				ev = append(ev, fmt.Sprintf("and %d more.", len(devDown)-5))
				break
			}
			ev = append(ev, fmt.Sprintf("%s is %s (score %d), last data %s.", d.Name, d.Health, d.Score, ago(in.Now, d.LastSeen)))
		}
		sev := "warning"
		if len(devDown)*2 >= len(in.Devices) && len(in.Devices) >= 2 {
			sev = "critical"
		}
		r.Findings = append(r.Findings, Finding{
			Problem:     fmt.Sprintf("%d device(s) stopped reporting while their gateway is alive", len(devDown)),
			Evidence:    ev,
			LikelyCause: "Likely at the device: the sensor, its wiring or the equipment it reads stopped answering while the gateway keeps running.",
			Severity:    sev,
			Recommended: "Check these devices and the equipment they read; compare with any open alerts below.",
			PossibleFix: Fix{Action: "No remote repair exists in the platform; a person must check the devices.", Policy: AutoNever},
		})
	}

	// 3. open alerts, worst first
	if len(in.Alerts) > 0 {
		al := append([]Alert(nil), in.Alerts...)
		sort.SliceStable(al, func(i, j int) bool {
			if rank[al[i].Severity] != rank[al[j].Severity] {
				return rank[al[i].Severity] > rank[al[j].Severity]
			}
			return al[i].At.After(al[j].At)
		})
		cnt := map[string]int{}
		for _, a := range al {
			cnt[a.Severity]++
		}
		ev := []string{fmt.Sprintf("%d open: %d critical, %d warning, %d other.", len(al), cnt["critical"], cnt["warning"], len(al)-cnt["critical"]-cnt["warning"])}
		for i, a := range al {
			if i == 5 {
				ev = append(ev, fmt.Sprintf("and %d more.", len(al)-5))
				break
			}
			ev = append(ev, fmt.Sprintf("[%s] %s (raised %s).", a.Severity, a.Message, ago(in.Now, &a.At)))
		}
		sev := "warning"
		if cnt["critical"] > 0 {
			sev = "critical"
		}
		cause := "Cause not determined from alerts alone."
		if len(devDown) > 0 || len(explained) > 0 {
			cause = "Possibly related to the devices that stopped reporting; the alerts and the outage overlap in this scope, which is correlation, not proof."
		}
		r.Findings = append(r.Findings, Finding{
			Problem:     fmt.Sprintf("%d open alert(s) in scope", len(al)),
			Evidence:    ev,
			LikelyCause: cause,
			Severity:    sev,
			Recommended: "Review the alerts, starting with the most severe; acknowledge them once someone is working on them.",
			PossibleFix: Fix{Action: "Acknowledge an alert after someone has reviewed it.", Policy: ApprovalRequired, Tool: "acknowledge_alert"},
		})
	}

	// 4. degraded but reporting
	var deg []Device
	for _, d := range in.Devices {
		if d.Health == "degraded" {
			deg = append(deg, d)
		}
	}
	if len(deg) > 0 {
		r.Findings = append(r.Findings, Finding{
			Problem:     fmt.Sprintf("%d device(s) report but with degraded health", len(deg)),
			Evidence:    []string{"Devices: " + names(deg, 5) + "."},
			LikelyCause: "Likely slow or irregular reporting or low-quality samples; the cause is not visible in this data.",
			Severity:    "info",
			Recommended: "Look at these devices' health factors if the problem persists.",
			PossibleFix: Fix{Action: "None automatic.", Policy: AutoNever},
		})
	}

	if len(r.Findings) == 0 {
		r.Findings = append(r.Findings, Finding{
			Problem:     "No problem found in what was examined",
			Evidence:    []string{fmt.Sprintf("%d devices, %d healthy; %d gateways reporting; no open alerts.", len(in.Devices), r.Counts.Healthy, len(in.Gateways))},
			LikelyCause: "None indicated by this data.",
			Severity:    "info",
			Recommended: "If something still looks wrong, tell me what you see; the items below were not examined.",
			PossibleFix: Fix{Action: "None.", Policy: AutoNever},
		})
	}
	sort.SliceStable(r.Findings, func(i, j int) bool { return rank[r.Findings[i].Severity] > rank[r.Findings[j].Severity] })
	r.Severity = r.Findings[0].Severity
	r.Examined = []string{"device health (freshness and sample quality)", "gateway status and last contact", "open alerts"}
	r.NotExamined = []string{"device-internal logs and protocol traces", "maintenance windows", "telemetry values against limits", "network equipment outside the platform"}
	return r
}
