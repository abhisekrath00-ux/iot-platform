package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"path/filepath"
	"sync"
	"time"

	"github.com/abhisekrath00-ux/iot-platform/edge/internal/autodetect"
	"github.com/abhisekrath00-ux/iot-platform/edge/internal/config"
	"github.com/abhisekrath00-ux/iot-platform/edge/internal/paths"
)

// Edge-side auto-detection: the agent looks for devices by itself, on first run
// and then periodically, and keeps proposals in <data>/discoveries.json. It needs
// no server; when the broker is reachable the proposals are also published on
// scan/result as "auto-" results so the dashboard Scan page lists them. Nothing
// is added to polling unless autodetect.auto_add is on, and then only confident
// matches. Reads only. See docs/edge-autodetect.md.

func discoveriesPath() string { return filepath.Join(paths.Data(), "discoveries.json") }
func candidatesPath() string  { return filepath.Join(paths.Data(), "scan-candidates.json") }
func profilesDir() string     { return filepath.Join(paths.Data(), "profiles.d") }

const detectLimit = 25 * time.Minute

// cfgMu guards the loaded config against reloads (fleet apply, auto-add).
var cfgMu sync.Mutex

func cfgSnapshot(cfg *config.Config) config.Config {
	cfgMu.Lock()
	defer cfgMu.Unlock()
	return *cfg
}

// detectOnce runs one pass and records it. Used by the loop and by -detect-now.
func detectOnce(ctx context.Context, cfg *config.Config, store *autodetect.Store, now func() time.Time) (autodetect.Result, []autodetect.Profile, []string) {
	profiles, warns := autodetect.LoadLibrary(profilesDir(), autodetect.LoadCandidates(candidatesPath()))
	snap := cfgSnapshot(cfg)
	d := autodetect.New(&snap, profiles)
	d.Now = now
	cctx, cancel := context.WithTimeout(ctx, detectLimit)
	defer cancel()
	res := d.Run(cctx)
	store.Merge(now().UTC(), res.Findings, append(warns, res.Notes...))
	return res, profiles, warns
}

// startAutodetect runs the loop. publish returns an error when the broker is not
// connected; a result that could not be published is retried every minute.
func startAutodetect(ctx context.Context, cfg *config.Config, store *autodetect.Store, publish func([]byte) error, reload func()) {
	if !cfg.Autodetect.IsEnabled() {
		log.Printf("autodetect: off (autodetect.enabled: false)")
		return
	}
	var pmu sync.Mutex
	var pending []autodetect.Payload
	flush := func() {
		pmu.Lock()
		defer pmu.Unlock()
		keep := pending[:0]
		for _, p := range pending {
			b, _ := json.Marshal(p)
			if err := publish(b); err != nil {
				keep = append(keep, p)
				continue
			}
			store.SetPublished(p.Scope(), p.Sum())
		}
		pending = keep
	}
	go func() {
		t := time.NewTicker(time.Minute)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				flush()
			}
		}
	}()
	go func() {
		wait := 45 * time.Second // let polling and the broker come up first
		for {
			select {
			case <-ctx.Done():
				return
			case <-time.After(wait):
			}
			wait = cfg.Autodetect.Every()
			if !scanMu.TryLock() {
				wait = 2 * time.Minute // a dashboard scan is running; try again soon
				continue
			}
			res, profiles, _ := detectOnce(ctx, cfg, store, time.Now)
			scanMu.Unlock()
			snap := cfgSnapshot(cfg)
			from, to := snap.Autodetect.Range()
			n := 0
			keep := store.Snapshot().Findings
			state := map[string]string{}
			for _, f := range keep {
				state[f.Key] = f.State
			}
			var visible []autodetect.Finding
			for _, f := range res.Findings {
				if state[f.Key] != "ignored" {
					visible = append(visible, f)
				}
			}
			for _, p := range autodetect.Payloads(visible, from, to, time.Now()) {
				if store.PublishedHash(p.Scope()) == p.Sum() {
					continue
				}
				pmu.Lock()
				pending = append(pending, p)
				pmu.Unlock()
				n++
			}
			flush()
			log.Printf("autodetect: %d finding(s), %d new result(s) for the server", len(res.Findings), n)
			if snap.Autodetect.AutoAdd {
				added, err := autodetect.AutoAdd(&snap, profiles, store.Snapshot().Findings, config.OverlayPath())
				if err != nil {
					log.Printf("autodetect: auto-add: %v", err)
				}
				for _, a := range added {
					log.Printf("autodetect: auto-added %s (%s address %d)", a.ID, a.Port, a.Address)
					store.MarkState(fmt.Sprintf("modbus-rtu:%s:%d%s:%d", a.Port, a.Baud, a.Parity, a.Address), "auto-added")
				}
				if len(added) > 0 {
					reload()
				}
			}
		}
	}()
}

// detectNowCLI is `edge-agent -detect-now`: one pass, a readable report, exit.
// It works with no server and no network beyond the local subnets.
func detectNowCLI(cfgPath string, out io.Writer) int {
	cfg, err := config.Load(cfgPath)
	if err != nil {
		fmt.Fprintln(out, "config:", err)
		return 1
	}
	store := autodetect.OpenStore(discoveriesPath())
	fmt.Fprintln(out, "scanning (read-only; serial sweeps can take several minutes)...")
	res, profiles, _ := detectOnce(context.Background(), cfg, store, time.Now)
	fmt.Fprintf(out, "%d profile(s) known to this gateway\n", len(profiles))
	printState(out, store.Snapshot())
	for _, n := range res.Notes {
		fmt.Fprintln(out, "note:", n)
	}
	return 0
}

// discoveriesCLI is `edge-agent -discoveries`: print what the last pass found.
func discoveriesCLI(out io.Writer) int {
	st := autodetect.OpenStore(discoveriesPath()).Snapshot()
	if st.LastRun.IsZero() {
		fmt.Fprintln(out, "no autodetect pass has run yet on this gateway (the agent runs one shortly after it starts; or use -detect-now)")
		return 0
	}
	printState(out, st)
	return 0
}

// ignoreCLI is `edge-agent -ignore KEY`. Keys are printed by -discoveries.
func ignoreCLI(key string, undo bool, out io.Writer) int {
	store := autodetect.OpenStore(discoveriesPath())
	found := false
	for _, f := range store.Snapshot().Findings {
		found = found || f.Key == key
	}
	if !found {
		fmt.Fprintln(out, "no such proposal; keys are listed by -discoveries")
		return 1
	}
	st := "ignored"
	if undo {
		st = "suggested"
	}
	store.MarkState(key, st)
	fmt.Fprintf(out, "%s: %s\n", key, st)
	return 0
}

func printState(out io.Writer, st autodetect.State) {
	fmt.Fprintf(out, "last run %s, %d proposal(s)\n", st.LastRun.Local().Format(time.RFC3339), len(st.Findings))
	for _, f := range st.Findings {
		line := ""
		switch f.Kind {
		case "modbus-rtu":
			line = fmt.Sprintf("serial %s %d/%s address %d", f.Port, f.Baud, f.Parity, f.Address)
			if len(f.Matches) > 0 {
				m := f.Matches[0]
				line += fmt.Sprintf(" - looks like %s (%d%% of %d registers plausible)", m.Name, int(m.Score*100), m.Probed)
			} else {
				line += " - answers, no profile matched"
			}
		case "lan":
			line = fmt.Sprintf("network %s:%d (%s)", f.Host, f.NetPort, f.Service)
		case "bacnet":
			line = fmt.Sprintf("BACnet %s device %d vendor %d", f.Host, f.Instance, f.Vendor)
		}
		conf := ""
		if f.Confident {
			conf = " [confident]"
		}
		fmt.Fprintf(out, "  %-10s %s%s\n             key: %s\n", f.State, line, conf, f.Key)
	}
	for _, n := range st.LastNote {
		fmt.Fprintln(out, "  note:", n)
	}
}
