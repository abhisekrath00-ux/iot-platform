package main

import "fmt"

// Case is one tool-selection question. Want lists the acceptable first tool calls; Arg, when set,
// must appear (case-insensitively) in the call's arguments. The questions are generated from
// phrasings x entities so coverage is broad, but they are templates, not a sample of real users.
type Case struct {
	Q    string
	Want []string
	Arg  string
}

func cases() []Case {
	var cs []Case
	add := func(want string, arg string, qs ...string) {
		for _, q := range qs {
			cs = append(cs, Case{Q: q, Want: []string{want}, Arg: arg})
		}
	}
	for _, d := range []string{"pump-1", "boiler-2", "chiller-7", "fan-3", "compressor-4"} {
		add("get_device_health", d,
			fmt.Sprintf("How healthy is %s?", d), fmt.Sprintf("Is %s reporting normally?", d), fmt.Sprintf("Health score for %s please", d))
		add("get_device_state", d, fmt.Sprintf("What is the current state of %s?", d))
		add("latest_values", d, fmt.Sprintf("What are the latest readings from %s?", d))
	}
	for _, a := range []string{"a12", "al-77", "alert-9"} {
		add("get_alert", a, fmt.Sprintf("Show me alert %s", a), fmt.Sprintf("Details of alert %s", a))
		add("explain_alert", a, fmt.Sprintf("Why did alert %s fire?", a), fmt.Sprintf("Explain alert %s", a))
		add("acknowledge_alert", a, fmt.Sprintf("Acknowledge alert %s", a), fmt.Sprintf("Ack %s for me", a))
		add("comment_on_alert", a, fmt.Sprintf("Add a comment to alert %s saying valve checked", a))
	}
	for _, q := range []string{"How many alerts are open?", "List open alerts", "Which alerts are active right now?", "Show unresolved alerts", "Any acknowledged alerts?"} {
		add("list_alerts", "", q)
	}
	for _, q := range []string{"Give me a fleet overview", "How is the whole fleet doing?", "Fleet health summary", "How many devices are online?", "What is the overall status?"} {
		add("fleet_summary", "", q)
	}
	for _, q := range []string{"List all devices", "Which devices do we have?", "Show devices in group g1", "What devices are registered?"} {
		add("list_devices", "", q)
	}
	for _, q := range []string{"List gateways", "Which gateways are connected?", "Are all gateways online?", "Show edge gateways"} {
		add("list_gateways", "", q)
	}
	for _, q := range []string{"List the assets", "Show the asset hierarchy", "What sites and assets exist?"} {
		add("list_assets", "", q)
	}
	for _, p := range []string{"temp", "pressure", "vibration"} {
		add("get_telemetry", p, fmt.Sprintf("Show %s of pump-1 for the last 6 hours", p), fmt.Sprintf("Chart %s on boiler-2 over 24 hours", p))
		add("find_anomalies", p, fmt.Sprintf("Any anomalies in %s on pump-1?", p), fmt.Sprintf("Is the %s on fan-3 behaving oddly?", p))
		add("forecast_point", p, fmt.Sprintf("Forecast %s for pump-1", p), fmt.Sprintf("Where is %s on chiller-7 heading?", p))
		add("related_signals", p, fmt.Sprintf("Which signals move together with %s on pump-1?", p))
	}
	for _, q := range []string{"How do I add a Modbus device?", "What does the platform say about air-gapped deployment?", "How do I set up SSO?", "Where is retention explained?", "How do I back up the database?", "What is the four-eyes approval?"} {
		add("search_docs", "", q)
	}
	for _, s := range []string{"north plant", "south plant", "boiler house", "warehouse"} {
		add("investigate_scope", s, fmt.Sprintf("Something seems wrong with the %s. Investigate it.", s), fmt.Sprintf("Check the %s for problems", s), fmt.Sprintf("What is going on at the %s?", s))
	}
	add("list_kpis", "", "List our KPIs", "What KPIs are defined?")
	add("list_groups", "", "Show device groups", "Which groups exist?")
	add("list_maintenance_windows", "", "Are there maintenance windows planned?", "Show maintenance windows")
	add("downstream_impact", "pump-1", "What depends on pump-1?", "If pump-1 fails, what is affected downstream?")
	add("list_commands", "", "Show recent control commands", "Which commands are waiting for approval?")
	add("list_rules", "", "List alert rules", "What rules are configured?")
	add("recent_audit", "", "What happened recently in the audit log?", "Show the latest audit entries")
	return cs
}
