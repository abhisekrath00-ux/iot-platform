//go:build linux

package setup

import (
	"bufio"
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"golang.org/x/sys/unix"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

type commissioningInput struct {
	Server, Serial, Code, TransportCA, DeviceCA, ExpectedTenant string
	ResponseKey                                                 ed25519.PublicKey
}

// Every component is opened no-follow. Returned descriptor stays pinned until close.
func commissionDirectory(path string) (*os.File, error) {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path || path == "/" {
		return nil, fmt.Errorf("canonical private directory required")
	}
	fd, e := unix.Open("/", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if e != nil {
		return nil, e
	}
	parts := strings.Split(strings.TrimPrefix(path, "/"), "/")
	for i, n := range parts {
		next, e := unix.Openat(fd, n, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
		unix.Close(fd)
		if e != nil {
			return nil, e
		}
		fd = next
		var st unix.Stat_t
		e = unix.Fstat(fd, &st)
		safe := e == nil && (st.Uid == uint32(os.Getuid()) || st.Uid == 0)
		if i == len(parts)-1 {
			safe = safe && st.Uid == uint32(os.Getuid()) && st.Mode&0077 == 0
		} else if st.Mode&0022 != 0 {
			safe = safe && st.Mode&unix.S_ISVTX != 0
		}
		if !safe {
			unix.Close(fd)
			return nil, fmt.Errorf("unsafe local directory")
		}
	}
	return os.NewFile(uintptr(fd), path), nil
}
func privateCommissioningFile(path string) ([]byte, error) {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return nil, fmt.Errorf("canonical private input path required")
	}
	d, e := commissionDirectory(filepath.Dir(path))
	if e != nil {
		return nil, fmt.Errorf("private input parent required")
	}
	defer d.Close()
	fd, e := unix.Openat(int(d.Fd()), filepath.Base(path), unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_CLOEXEC|unix.O_NONBLOCK, 0)
	if e != nil {
		return nil, fmt.Errorf("private input unavailable")
	}
	f := os.NewFile(uintptr(fd), "private-input")
	defer f.Close()
	var st unix.Stat_t
	if unix.Fstat(fd, &st) != nil || st.Mode&unix.S_IFMT != unix.S_IFREG || st.Mode&0777 != 0600 || int(st.Uid) != os.Getuid() || st.Nlink != 1 || st.Size > 131072 {
		return nil, fmt.Errorf("owner-only regular single-link bounded input required")
	}
	raw, e := io.ReadAll(io.LimitReader(f, 131073))
	if e != nil || len(raw) > 131072 {
		return nil, fmt.Errorf("bounded input required")
	}
	return raw, nil
}
func parseCommissioning(raw []byte) (commissioningInput, error) {
	var v commissioningInput
	if e := decodeMappingInput(raw, &v); e != nil {
		return v, e
	}
	u, e := url.Parse(v.Server)
	if e != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Path != "" || u.ForceQuery || v.Serial == "" || len(v.Serial) > 128 || v.Code == "" || len(v.Code) > 4096 || v.ExpectedTenant == "" || len(v.ExpectedTenant) > 128 || len(v.ResponseKey) != ed25519.PublicKeySize {
		return v, fmt.Errorf("explicit HTTPS identity and public trust required")
	}
	for _, b := range []string{v.TransportCA, v.DeviceCA} {
		r := x509.NewCertPool()
		if !r.AppendCertsFromPEM([]byte(b)) {
			return v, fmt.Errorf("public trust unavailable")
		}
	}
	return v, nil
}
func commissionHash(b []byte) string { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }

type cappedCommissioningOutput struct {
	bytes.Buffer
	overflow bool
}

func (b *cappedCommissioningOutput) Write(p []byte) (int, error) {
	n := len(p)
	if b.Len()+n > 8192 {
		b.overflow = true
		return n, nil
	}
	return b.Buffer.Write(p)
}
func commissionChild(flag string, body any, want string) error {
	raw, e := json.Marshal(body)
	if e != nil {
		return e
	}
	exe, e := os.Open("/proc/self/exe")
	if e != nil {
		return e
	}
	defer exe.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "/proc/self/fd/3", flag)
	cmd.ExtraFiles = []*os.File{exe}
	cmd.Stdin = bytes.NewReader(raw)
	var out cappedCommissioningOutput
	cmd.Stdout = &out
	cmd.Stderr = io.Discard
	if e = cmd.Run(); e != nil || out.overflow {
		return fmt.Errorf("operation unresolved; preserve all private state, never replay create")
	}
	var result struct {
		State string `json:"state"`
	}
	if json.Unmarshal(out.Bytes(), &result) != nil || result.State != want {
		return fmt.Errorf("unverified summary; preserve all private state")
	}
	return nil
}

// RunCommissioningImport exposes only fixed existing private operations. It is
// not a service manager and never infers permission for another step.
func RunCommissioningImport(input io.Reader, output io.Writer) error {
	if os.Geteuid() == 0 {
		return fmt.Errorf("commissioning must run as your non-root user; no input was read")
	}
	r := bufio.NewReader(io.LimitReader(input, 32768))
	ask := func(prompt string) (string, error) {
		fmt.Fprintln(output, prompt)
		s, e := r.ReadString('\n')
		return strings.TrimSpace(s), e
	}
	path, e := ask("Private handoff file path:")
	if e != nil {
		return fmt.Errorf("cancelled")
	}
	raw, e := privateCommissioningFile(path)
	if e != nil {
		return e
	}
	v, e := parseCommissioning(raw)
	if e != nil {
		return e
	}
	fmt.Fprintf(output, "Review origin %q, tenant %q, serial %q. No installation or service action.\n", v.Server, v.ExpectedTenant, v.Serial)
	for i, b := range [][]byte{[]byte(v.TransportCA), []byte(v.DeviceCA), v.ResponseKey} {
		names := []string{"transport PEM", "device PEM", "result public key"}
		got := commissionHash(b)
		fmt.Fprintf(output, "%s SHA256 %s\n", names[i], got)
		expected, e := ask("Enter independently obtained authenticated server SHA256 (not from this file):")
		if e != nil || expected != got {
			return fmt.Errorf("independent trust comparison refused")
		}
	}
	held := map[string]*os.File{}
	defer func() {
		for _, f := range held {
			f.Close()
		}
	}()
	pin := func(path string) error {
		f, e := commissionDirectory(path)
		if e != nil {
			return e
		}
		if old, ok := held[path]; ok {
			a, _ := old.Stat()
			b, _ := f.Stat()
			f.Close()
			if a == nil || b == nil || !os.SameFile(a, b) {
				return fmt.Errorf("reviewed path changed")
			}
			return nil
		}
		held[path] = f
		return nil
	}
	check := func() error {
		for p, f := range held {
			current, e := commissionDirectory(p)
			if e != nil {
				return fmt.Errorf("reviewed path changed")
			}
			a, _ := f.Stat()
			b, _ := current.Stat()
			current.Close()
			if a == nil || b == nil || !os.SameFile(a, b) {
				return fmt.Errorf("reviewed path changed; preserve all state")
			}
		}
		return nil
	}
	dir, e := ask("Local retained folder (NEW for create, SAME for recover/accept), absolute private parent:")
	if e != nil || !filepath.IsAbs(dir) || filepath.Clean(dir) != dir || dir == "/" {
		return fmt.Errorf("canonical local destination required")
	}
	if e = pin(filepath.Dir(dir)); e != nil {
		return e
	}
	action, e := ask("Type CREATE BOUND, RECOVER SAME, or ACCEPT PRIVATE. Anything else cancels:")
	if e != nil {
		return fmt.Errorf("cancelled")
	}
	var wire map[string]any
	json.Unmarshal(raw, &wire)
	wire["Dir"] = dir
	flag, want := "", ""
	switch action {
	case "CREATE BOUND":
		prepared, e := ask("NEW private prepared payload folder (absolute, private parent):")
		if e != nil || !filepath.IsAbs(prepared) || filepath.Clean(prepared) != prepared {
			return fmt.Errorf("canonical preparation required")
		}
		if e = pin(filepath.Dir(prepared)); e != nil {
			return e
		}
		acceptParent, e := ask("Existing private identity destination parent (separate from retained/prepared roots):")
		if e != nil {
			return fmt.Errorf("cancelled")
		}
		if e = pin(acceptParent); e != nil {
			return e
		}
		leaf, e := ask("NEW identity destination leaf:")
		if e != nil || leaf == "" || leaf == "." || leaf == ".." || filepath.Base(leaf) != leaf {
			return fmt.Errorf("exact destination leaf required")
		}
		for _, pair := range [][2]string{{dir, prepared}, {dir, acceptParent}, {prepared, acceptParent}} {
			for _, p := range [][2]string{pair, {pair[1], pair[0]}} {
				rel, e := filepath.Rel(p[0], p[1])
				if e != nil || rel == "." || rel != ".." && !strings.HasPrefix(rel, "../") {
					return fmt.Errorf("private roots must be disjoint")
				}
			}
		}
		fmt.Fprintf(output, "Retained %q; preparation %q; accepted identity %q. Nothing installed or started.\n", dir, prepared, filepath.Join(acceptParent, leaf))
		confirm, e := ask("Type PREPARE NEW PRIVATE PAYLOAD or REVIEW EXISTING PRIVATE PAYLOAD. Anything else cancels:")
		if e != nil {
			return fmt.Errorf("cancelled")
		}
		if e = check(); e != nil {
			return e
		}
		if confirm == "PREPARE NEW PRIVATE PAYLOAD" {
			e = commissionChild("-prepare-private-install", map[string]string{"Dir": prepared}, "private_payload_prepared")
		} else if confirm == "REVIEW EXISTING PRIVATE PAYLOAD" {
			if e = pin(prepared); e != nil {
				return e
			}
			e = commissionChild("-review-private-install", map[string]string{"Dir": prepared}, "private_payload_verified_not_installed")
		} else {
			return fmt.Errorf("cancelled, nothing sent")
		}
		if e != nil {
			return e
		}
		if e = pin(prepared); e != nil {
			return e
		}
		if e = pin(filepath.Join(prepared, "prepared-state")); e != nil {
			return e
		}
		confirm, e = ask("Private payload ready only. Type CREATE BOUND ATTEMPT to send one claim. Anything else cancels and preserves payload:")
		if e != nil || confirm != "CREATE BOUND ATTEMPT" {
			return fmt.Errorf("cancelled; private preparation preserved, no claim sent")
		}
		wire["Targets"] = map[string]string{"Binary": filepath.Join(prepared, "prepared-agent"), "Config": filepath.Join(prepared, "prepared-config.yaml"), "State": filepath.Join(prepared, "prepared-state"), "Unit": filepath.Join(prepared, "prepared-unit.service"), "Destination": filepath.Join(acceptParent, leaf)}
		flag = "-create-staged-install"
		want = "verified_install_attempt_retained"
	case "RECOVER SAME":
		if e = pin(dir); e != nil {
			return e
		}
		flag = "-recover-retained-claim"
		want = "verified_recovery_retained"
	case "ACCEPT PRIVATE":
		if e = pin(dir); e != nil {
			return e
		}
		parent, e := ask("Exact prebound private identity parent:")
		if e != nil {
			return fmt.Errorf("cancelled")
		}
		if e = pin(parent); e != nil {
			return e
		}
		leaf, e := ask("Exact prebound identity leaf:")
		if e != nil {
			return fmt.Errorf("cancelled")
		}
		fmt.Fprintf(output, "Accept only exact prebound private identity %q, retained %q. No install or service action.\n", filepath.Join(parent, leaf), dir)
		confirm, e := ask("Type ACCEPT PREBOUND PRIVATE IDENTITY. Anything else cancels:")
		if e != nil || confirm != "ACCEPT PREBOUND PRIVATE IDENTITY" {
			return fmt.Errorf("cancelled, no acceptance sent")
		}
		wire["DestinationParent"] = parent
		wire["DestinationName"] = leaf
		flag = "-accept-private-identity"
		want = "private_identity_accepted_service_blocked"
	default:
		return fmt.Errorf("cancelled, nothing sent")
	}
	if e = check(); e != nil {
		return e
	}
	if e = commissionChild(flag, wire, want); e != nil {
		return e
	}
	if e = check(); e != nil {
		return e
	}
	fmt.Fprintf(output, "%s. Private handoff only. No install, service start, broker or telemetry proof.\n", want)
	return nil
}
