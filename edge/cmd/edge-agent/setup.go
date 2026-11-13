package main

import (
	"context"
	"fmt"
	"go.bug.st/serial"
	"io"
	"net/http"
	"os"
	"time"

	"github.com/abhisekrath00-ux/iot-platform/edge/internal/claim"
	"github.com/abhisekrath00-ux/iot-platform/edge/internal/setup"
)

// fetchSetup writes only a new staging file. The native manager uses its shared
// checked import/backup path afterwards. No running service or old config is touched.
func fetchSetup(server, serial, identityDir, output string, managed bool, out io.Writer) int {
	if output == "" {
		fmt.Fprintln(out, "Choose a staging output file; existing config is never overwritten by download.")
		return 1
	}
	if _, err := os.Stat(output); err == nil {
		fmt.Fprintln(out, "Staging file already exists; no files changed.")
		return 1
	} else if !os.IsNotExist(err) {
		fmt.Fprintln(out, "Cannot inspect staging destination.")
		return 1
	}
	id, err := claim.LoadIdentity(identityDir)
	if err != nil {
		fmt.Fprintln(out, "Saved identity is unavailable. Do not create a replacement claim.")
		return 1
	}
	if err := setup.ValidateIdentity(identityDir, serial); err != nil {
		fmt.Fprintln(out, err)
		return 1
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	b, err := setup.Fetch(ctx, &http.Client{Timeout: 15 * time.Second}, server, id, serial, managed)
	if err != nil {
		fmt.Fprintln(out, err)
		return 1
	}
	f, err := os.OpenFile(output, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		fmt.Fprintln(out, "Cannot create staging file; no existing files changed.")
		return 1
	}
	ok := false
	defer func() {
		f.Close()
		if !ok {
			os.Remove(output)
		}
	}()
	if _, err = f.Write(b); err != nil {
		fmt.Fprintln(out, "Cannot save staging file.")
		return 1
	}
	if err = f.Sync(); err != nil {
		fmt.Fprintln(out, "Cannot flush staging file.")
		return 1
	}
	if err = f.Close(); err != nil {
		return 1
	}
	if checkConfig(output, identityDir, out) != 0 {
		return 1
	}
	ok = true
	fmt.Fprintln(out, "Server configuration downloaded and checked. It has not been imported, started, or proven connected.")
	return 0
}

func securePairing(finish bool, server, serial, dir string, out io.Writer) int {
	id, e := claim.LoadIdentity(dir)
	if e != nil {
		fmt.Fprintln(out, "Saved identity unavailable; do not claim again.")
		return 1
	}
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	client := &http.Client{Timeout: 15 * time.Second}
	if !finish {
		req, e := setup.PrepareMigration(ctx, client, server, dir, serial, id)
		if e != nil {
			fmt.Fprintln(out, e)
			return 1
		}
		fmt.Fprintf(out, "Pairing request %s (%s)\nCompare this public-key fingerprint in the dashboard:\n%s\nPrivate key and identity unchanged. Waiting for explicit workspace administrator approval.\n", req.ID, req.State, req.KeySHA256)
		return 0
	}
	bundle, e := setup.FetchMigration(ctx, client, server, dir, serial, id)
	if e != nil {
		fmt.Fprintln(out, e)
		return 1
	}
	if e = setup.FinishMigration(ctx, client, server, dir, serial, id, bundle); e != nil {
		fmt.Fprintln(out, e)
		return 1
	}
	fmt.Fprintln(out, "Certificate verified and installed after the server observed a fresh own-gateway broker proof. No device command or meter read was sent. Saved key, gateway identity and config retained. Connect / get config next; service remains stopped.")
	return 0
}

func reportSetupPorts(server, dir string, out io.Writer) int {
	id, e := claim.LoadIdentity(dir)
	if e != nil {
		fmt.Fprintln(out, "Saved gateway identity unavailable; do not claim again.")
		return 1
	}
	ports, e := serial.GetPortsList()
	if e != nil {
		fmt.Fprintln(out, "OS serial port listing failed; no probe sent.")
		return 1
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if e = setup.ReportSerialPorts(ctx, &http.Client{Timeout: 15 * time.Second}, server, id, ports); e != nil {
		fmt.Fprintln(out, e)
		return 1
	}
	fmt.Fprintf(out, "Reported %d OS serial ports to this gateway's dashboard. No port opened, probed or selected. Choose the meter adapter's port in source setup.\n", len(ports))
	return 0
}
