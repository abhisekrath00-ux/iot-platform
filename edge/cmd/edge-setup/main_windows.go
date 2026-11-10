// HexThings Setup is a Windows installer/manager launcher, not the agent service.
package main

import (
	"archive/zip"
	"bytes"
	"embed"
	"fmt"
	"golang.org/x/sys/windows"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

//go:embed payload
var payload embed.FS

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "HexThings setup failed:", err)
		os.Exit(1)
	}
}
func run() error {
	self, err := os.Executable()
	if err != nil {
		return err
	}
	if !windows.GetCurrentProcessToken().IsElevated() {
		cmd := exec.Command("powershell.exe", "-NoProfile", "-Command", `$ErrorActionPreference='Stop'; try { $p=Start-Process -FilePath $env:HEXTHINGS_SETUP_EXE -Verb RunAs -Wait -PassThru; exit $p.ExitCode } catch { Write-Error $_; exit 1 }`)
		cmd.Env = append(os.Environ(), "HEXTHINGS_SETUP_EXE="+self)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		return cmd.Run()
	}
	raw, err := payload.ReadFile("payload/package.zip")
	if err != nil {
		return fmt.Errorf("no bundled installer payload: %w", err)
	}
	z, err := zip.NewReader(bytes.NewReader(raw), int64(len(raw)))
	if err != nil {
		return err
	}
	dir, err := os.MkdirTemp(os.Getenv("ProgramFiles"), "HexThings-Setup-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	// Extract only after elevation into an administrator-protected directory, not user TEMP.
	for _, args := range [][]string{{dir, "/inheritance:r", "/grant:r", "*S-1-5-18:(OI)(CI)F", "*S-1-5-32-544:(OI)(CI)F"}, {dir, "/setintegritylevel", "(OI)(CI)H"}} {
		if err := exec.Command("icacls.exe", args...).Run(); err != nil {
			return fmt.Errorf("protect installer files: %w", err)
		}
	}
	for _, f := range z.File {
		// Packaging owns this archive. Still reject path traversal and symlinks.
		if f.FileInfo().IsDir() {
			continue
		}
		if f.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("linked payload refused")
		}
		name := filepath.Base(f.Name)
		if name != f.Name || strings.ContainsAny(name, "/\\:") {
			return fmt.Errorf("invalid payload path")
		}
		if f.UncompressedSize64 > 100<<20 {
			return fmt.Errorf("payload too large")
		}
		r, e := f.Open()
		if e != nil {
			return e
		}
		p, e := os.OpenFile(filepath.Join(dir, name), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if e != nil {
			r.Close()
			return e
		}
		_, e = io.Copy(p, io.LimitReader(r, 100<<20))
		ce := p.Close()
		r.Close()
		if e != nil {
			return e
		}
		if ce != nil {
			return ce
		}
	}
	script := filepath.Join(dir, "setup-wizard.ps1")
	if _, err = os.Stat(script); err != nil {
		return err
	}
	cmd := exec.Command("powershell.exe", "-NoProfile", "-STA", "-ExecutionPolicy", "Bypass", "-File", script)
	cmd.Env = append(os.Environ(), "HEXTHINGS_SETUP_SCRIPT="+script, "HEXTHINGS_SETUP_EXE="+self)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin
	return cmd.Run()
}
