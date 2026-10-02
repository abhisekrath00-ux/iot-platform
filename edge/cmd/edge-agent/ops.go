package main

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"go.bug.st/serial"

	"github.com/abhisekrath00-ux/iot-platform/edge/internal/config"
	"github.com/abhisekrath00-ux/iot-platform/edge/internal/logfile"
	"github.com/abhisekrath00-ux/iot-platform/edge/internal/paths"
)

// setupLogFile sends the standard logger to a rotating file. An explicit path
// wins; otherwise a Windows service (which has no console or journal) logs to
// <data dir>/logs/edge-agent.log. Returns a closer.
func setupLogFile(explicit string) func() {
	path := explicit
	if path == "" {
		path = os.Getenv("HEXMON_LOG_FILE")
	}
	if path == "" && runtime.GOOS == "windows" && runningAsService() {
		path = filepath.Join(paths.Data(), "logs", "edge-agent.log")
	}
	if path == "" {
		return func() {}
	}
	w, err := logfile.Open(path, 5<<20, 5)
	if err != nil {
		log.Printf("log file %s: %v (continuing with stderr)", path, err)
		return func() {}
	}
	log.SetOutput(io.MultiWriter(os.Stderr, w))
	return func() { _ = w.Close() }
}

// checkConfig loads and lints a config file, prints findings, and returns the
// process exit code: 0 clean or warnings only, 1 on errors.
func checkConfig(path, identityDir string, out io.Writer) int {
	c, err := config.Load(path)
	if err != nil {
		fmt.Fprintf(out, "ERROR: %v\n", err)
		return 1
	}
	c.DefaultIdentityFiles(identityDir)
	is := config.Lint(c, runtime.GOOS)
	for _, i := range is {
		fmt.Fprintln(out, i.String())
	}
	if config.HasError(is) {
		fmt.Fprintf(out, "%s: %d finding(s), not usable until the errors are fixed\n", path, len(is))
		return 1
	}
	fmt.Fprintf(out, "%s: OK (%d device(s), %d warning(s))\n", path, len(c.Devices), len(is))
	return 0
}

// listPorts prints the serial ports this OS reports, with a permission hint on Linux.
func listPorts(out io.Writer) int {
	ports, err := serial.GetPortsList()
	if err != nil {
		fmt.Fprintf(out, "cannot list serial ports: %v\n", err)
		return 1
	}
	if len(ports) == 0 {
		fmt.Fprintln(out, "no serial ports found.")
		if runtime.GOOS == "linux" {
			fmt.Fprintln(out, "USB adapters appear as /dev/ttyUSB* or /dev/ttyACM*; check `dmesg | tail` after plugging one in.")
		}
		return 0
	}
	for _, p := range ports {
		line := p
		if runtime.GOOS == "linux" {
			if f, err := os.OpenFile(p, os.O_RDWR|syscallNoCtty(), 0); err != nil {
				line += "  (cannot open: " + err.Error() + "; add the service user to the dialout group)"
			} else {
				f.Close()
				line += "  (openable by this user)"
			}
		}
		fmt.Fprintln(out, line)
	}
	if runtime.GOOS == "linux" {
		if ids, _ := filepath.Glob("/dev/serial/by-id/*"); len(ids) > 0 {
			fmt.Fprintln(out, "stable names (prefer these in config):")
			for _, id := range ids {
				fmt.Fprintln(out, "  "+id)
			}
		}
	}
	return 0
}

// healthCheck asks the running agent's local status page. Exit codes: 0 healthy,
// 1 agent not reachable, 2 running but not connected to the broker.
func healthCheck(cfgPath string, out io.Writer) int {
	addr := "127.0.0.1:8088"
	if c, err := config.Load(cfgPath); err == nil && c.UI.Listen != "" {
		if c.UI.Listen == "off" {
			fmt.Fprintln(out, "unknown: ui.listen is off, so there is no local status endpoint to check")
			return 1
		}
		addr = c.UI.Listen
	}
	if h, p, err := net.SplitHostPort(addr); err == nil && (h == "" || h == "0.0.0.0" || h == "::") {
		addr = net.JoinHostPort("127.0.0.1", p)
	}
	cl := &http.Client{Timeout: 3 * time.Second}
	resp, err := cl.Get("http://" + addr + "/api/status")
	if err != nil {
		fmt.Fprintf(out, "DOWN: %v\n", err)
		return 1
	}
	defer resp.Body.Close()
	var st struct {
		Version    string `json:"version"`
		Connected  bool   `json:"connected"`
		QueueDepth int    `json:"queue_depth"`
		UptimeSec  int64  `json:"uptime_seconds"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&st); err != nil || resp.StatusCode != 200 {
		fmt.Fprintf(out, "DOWN: status endpoint returned %d\n", resp.StatusCode)
		return 1
	}
	state := "OK"
	code := 0
	if !st.Connected {
		state, code = "DEGRADED (not connected to the broker; readings are being buffered)", 2
	}
	fmt.Fprintf(out, "%s version=%s uptime=%ds queued=%d\n", strings.TrimSpace(state), st.Version, st.UptimeSec, st.QueueDepth)
	return code
}
