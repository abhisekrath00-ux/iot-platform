//go:build linux

package driver

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"go.bug.st/serial"
	"golang.org/x/sys/unix"
)

func syntheticPTY(t *testing.T) string {
	t.Helper()
	fd, err := unix.Open("/dev/ptmx", unix.O_RDWR|unix.O_NOCTTY, 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { unix.Close(fd) })
	if err := unix.IoctlSetPointerInt(fd, unix.TIOCSPTLCK, 0); err != nil {
		t.Fatal(err)
	}
	n, err := unix.IoctlGetInt(fd, unix.TIOCGPTN)
	if err != nil {
		t.Fatal(err)
	}
	return fmt.Sprintf("/dev/pts/%d", n)
}
func TestSerialOwnerIdentityAliasBusyAndRelease(t *testing.T) {
	path := syntheticPTY(t)
	alias := filepath.Join(t.TempDir(), "alias")
	if err := os.Symlink(path, alias); err != nil {
		t.Fatal(err)
	}
	mode := &serial.Mode{BaudRate: 9600, DataBits: 8, StopBits: serial.OneStopBit}
	p, err := OpenOwnedSerial(path, mode)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := OpenOwnedSerial(alias, mode); err == nil || !strings.Contains(err.Error(), "already owned") {
		t.Fatal("alias stole ownership", err)
	}
	// All production driver/probe paths must use the same owner reservation.
	res := RunProbe(context.Background(), nil, ProbeRequest{Port: alias, Baud: 9600, DataBits: 8, StopBits: 1, Parity: "even", Address: 1, Func: 3, Register: 0, Count: 1, Type: "u16", WordOrder: "abcd"})
	if res.OK || !strings.Contains(res.Error, "already owned") {
		t.Fatal("probe escaped existing driver", res)
	}
	if err := p.Close(); err != nil {
		t.Fatal(err)
	}
	if err := p.Close(); err != nil {
		t.Fatal(err)
	}
	q, err := OpenOwnedSerial(alias, mode)
	if err != nil {
		t.Fatal("close failed to release", err)
	}
	q.Close()
}
func TestSerialOwnerRaceOneWinner(t *testing.T) {
	path := syntheticPTY(t)
	start := make(chan struct{})
	release := make(chan struct{})
	attempted := make(chan bool, 20)
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			p, err := OpenOwnedSerial(path, &serial.Mode{BaudRate: 9600, DataBits: 8})
			attempted <- err == nil
			if err == nil {
				<-release
				p.Close()
			}
		}()
	}
	close(start)
	winners := 0
	for i := 0; i < 20; i++ {
		select {
		case won := <-attempted:
			if won {
				winners++
			}
		case <-time.After(5 * time.Second):
			close(release)
			t.Fatal("ownership contenders did not finish")
		}
	}
	close(release)
	wg.Wait()
	if winners != 1 {
		t.Fatal("not exactly one owner", winners)
	}
}
func TestSerialOwnerRejectsOrdinaryFileAndReleasesFailedOpen(t *testing.T) {
	p := filepath.Join(t.TempDir(), "not-a-port")
	os.WriteFile(p, []byte("retained"), 0600)
	if _, err := OpenOwnedSerial(p, &serial.Mode{BaudRate: 9600}); err == nil {
		t.Fatal("file accepted")
	}
	b, _ := os.ReadFile(p)
	if string(b) != "retained" {
		t.Fatal("file changed")
	}
	// /dev/null passes the character-device identity fence, but the serial
	// library cannot obtain terminal settings. Every such failure must unlock.
	for i := 0; i < 2; i++ {
		if _, err := OpenOwnedSerial("/dev/null", &serial.Mode{BaudRate: 9600, DataBits: 8}); err == nil || strings.Contains(err.Error(), "already owned") {
			t.Fatal("failed native open retained ownership", err)
		}
	}
	path := syntheticPTY(t)
	if _, err := OpenOwnedSerial(path, &serial.Mode{BaudRate: -1}); err == nil {
		t.Fatal("bad mode accepted")
	}
	q, err := OpenOwnedSerial(path, &serial.Mode{BaudRate: 9600, DataBits: 8})
	if err != nil {
		t.Fatal("failed open retained lease", err)
	}
	q.Close()
}
func TestSerialDeviceLeaseOtherProcessAndCrashRelease(t *testing.T) {
	if path := os.Getenv("HEXTHINGS_TEST_SERIAL_LEASE_CHILD"); path != "" {
		key, canonical, err := serialIdentity(path)
		if err != nil {
			os.Exit(21)
		}
		unlock, err := serialLease(canonical, key)
		if err != nil {
			os.Exit(22)
		}
		defer unlock()
		fmt.Println("held")
		select {}
	}
	path := syntheticPTY(t)
	cmd := exec.Command(os.Args[0], "-test.run=^TestSerialDeviceLeaseOtherProcessAndCrashRelease$")
	cmd.Env = append(os.Environ(), "HEXTHINGS_TEST_SERIAL_LEASE_CHILD="+path)
	out, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { cmd.Process.Kill(); cmd.Wait() }()
	buf := make([]byte, 5)
	if n, err := io.ReadFull(out, buf); err != nil || string(buf[:n]) != "held\n" {
		t.Fatal("child did not acquire lease", n, err)
	}
	if _, err := OpenOwnedSerial(path, &serial.Mode{BaudRate: 9600, DataBits: 8}); err == nil {
		t.Fatal("cooperating process lease bypassed")
	}
	cmd.Process.Kill()
	cmd.Wait()
	p, err := OpenOwnedSerial(path, &serial.Mode{BaudRate: 9600, DataBits: 8})
	if err != nil {
		t.Fatal("process death did not release lease", err)
	}
	p.Close()
}
