package processingschema

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/abhisekrath00-ux/iot-platform/server/internal/auth"
	"github.com/golang-jwt/jwt/v5"
)

// Optional actual-binary compatibility proof. No owner endpoint or effects.
// This is not immutable image/full-background/restore acceptance.
func TestStagedPreviousCurrentPreviousAPIsAndOperator(t *testing.T) {
	oldAPI, newAPI, operator := os.Getenv("TEST_PROCESSING_PREVIOUS_API"), os.Getenv("TEST_PROCESSING_CURRENT_API"), os.Getenv("TEST_PROCESSING_SETUPCTL")
	oldDir := os.Getenv("TEST_PROCESSING_PREVIOUS_MIGRATIONS")
	if oldAPI == "" || newAPI == "" || operator == "" || oldDir == "" {
		t.Skip("run scripts/test-processing-schema-compatibility.sh")
	}
	p := db(t)
	ctx := t.Context()
	cfg := p.Config().ConnConfig.Copy()
	u, err := url.Parse(os.Getenv("TEST_DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	u.Path = "/" + cfg.Database
	dsn := u.String()
	if _, err := p.Exec(ctx, `INSERT INTO users(id,tenant_id,email,display_name,role) VALUES('processing-user','processing-retain','processing@example.invalid','Synthetic','admin');INSERT INTO device_profiles(id,tenant_id,name,driver_profile,points,created_by) VALUES('processing-profile','processing-retain','Processing keep source','modbus-generic','[]','processing-user');INSERT INTO points(id,device_id,unit) VALUES('temp','processing-dev','C')`); err != nil {
		t.Fatal(err)
	}
	newDir, err := filepath.Abs(coreDir())
	if err != nil {
		t.Fatal(err)
	}
	secret := "synthetic-processing-compatibility-only"
	token, err := jwt.NewWithClaims(jwt.SigningMethodHS256, auth.Claims{TenantID: "processing-retain", Role: "admin", RegisteredClaims: jwt.RegisteredClaims{Subject: "processing-user", ExpiresAt: jwt.NewNumericDate(time.Now().Add(10 * time.Minute))}}).SignedString([]byte(secret))
	if err != nil {
		t.Fatal(err)
	}
	launch := func(binary, dir, label string) {
		t.Helper()
		l, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		port := l.Addr().(*net.TCPAddr).Port
		l.Close()
		logfile, err := os.Create(filepath.Join(t.TempDir(), label+".log"))
		if err != nil {
			t.Fatal(err)
		}
		defer logfile.Close()
		cmd := exec.Command(binary)
		cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + t.TempDir(), "DATABASE_URL=" + dsn, "MIGRATIONS_DIR=" + dir, "API_PORT=" + fmt.Sprint(port), "JWT_SIGNING_SECRET=" + secret, "MQTT_TLS=false", "MQTT_HOST=127.0.0.1", "MQTT_PORT=1", "LOCAL_LOGIN=1"}
		cmd.Stdout = logfile
		cmd.Stderr = logfile
		if err = cmd.Start(); err != nil {
			t.Fatal(err)
		}
		done := make(chan error, 1)
		go func() { done <- cmd.Wait() }()
		defer func() {
			cmd.Process.Signal(syscall.SIGTERM)
			select {
			case <-done:
			case <-time.After(5 * time.Second):
				cmd.Process.Kill()
				<-done
			}
		}()
		client := &http.Client{Timeout: time.Second}
		base := fmt.Sprintf("http://127.0.0.1:%d", port)
		ready := false
		deadline := time.Now().Add(15 * time.Second)
		for time.Now().Before(deadline) {
			r, e := client.Get(base + "/healthz")
			if e == nil {
				io.Copy(io.Discard, r.Body)
				r.Body.Close()
				if r.StatusCode == 200 {
					ready = true
					break
				}
			}
			time.Sleep(50 * time.Millisecond)
		}
		if !ready {
			b, _ := os.ReadFile(logfile.Name())
			t.Fatalf("%s not healthy: %s", label, b)
		}
		request := func(method, path, body string) []byte {
			t.Helper()
			req, _ := http.NewRequest(method, base+path, strings.NewReader(body))
			req.Header.Set("Authorization", "Bearer "+token)
			req.Header.Set("Content-Type", "application/json")
			r, e := client.Do(req)
			if e != nil {
				t.Fatal(e)
			}
			defer r.Body.Close()
			b, e := io.ReadAll(r.Body)
			if e != nil || r.StatusCode != 200 {
				t.Fatalf("%s %s status%d: %s %v", label, path, r.StatusCode, b, e)
			}
			return b
		}
		for _, check := range []struct{ path, want string }{{"/v1/gateways", "PROCESSING-KEEP"}, {"/v1/profiles", "Processing keep source"}, {"/v1/devices", "Keep"}} {
			if !bytes.Contains(request("GET", check.path, ""), []byte(check.want)) {
				t.Fatal(label, check.path)
			}
		}
		body := fmt.Sprintf(`{"device_id":"processing-dev","readings":[{"point":"temp","value":42,"ts":%q,"event_id":%q}]}`, time.Now().UTC().Format(time.RFC3339Nano), label)
		for range 2 {
			var result struct {
				Accepted int `json:"accepted"`
			}
			if json.Unmarshal(request("POST", "/v1/telemetry/ingest", body), &result) != nil {
				t.Fatal(label, "bad telemetry response")
			}
		}
		var count int
		if err := p.QueryRow(ctx, "SELECT count(*) FROM telemetry WHERE event_id=$1", label).Scan(&count); err != nil || count != 1 {
			t.Fatal(label, count, err)
		}
	}
	launch(oldAPI, oldDir, "previous-before94")
	// Close the fixture reader so the actual local operator's live-client fence
	// can require exclusive maintenance. Reopen via the same lazy pool afterward.
	p.Reset()
	op := exec.Command(operator, "apply-staged-processing")
	op.Env = []string{"PATH=" + os.Getenv("PATH"), "DATABASE_URL=" + dsn, "MIGRATIONS_DIR=" + newDir}
	op.Stdin = strings.NewReader(`{"version":1,"scope":"staged_processing_v1","writers_stopped":true,"backup_verified":true}`)
	b, err := op.CombinedOutput()
	if err != nil {
		t.Fatalf("actual staged operator: %s %v", b, err)
	}
	if !bytes.Contains(b, []byte(`"processing_enabled":false`)) || !bytes.Contains(b, []byte(`"runtime_accepted":false`)) {
		t.Fatal("operator claimed activation", string(b))
	}
	assertRetained(t, p, 94)
	launch(newAPI, newDir, "current-after94")
	launch(oldAPI, oldDir, "previous-on94")
	launch(newAPI, newDir, "current-again94")
	assertRetained(t, p, 94)
	var n int
	if err = p.QueryRow(ctx, "SELECT count(*) FROM ingest_processing_v1").Scan(&n); err != nil || n != 0 {
		t.Fatal("unexpected runtime processing", n, err)
	}
	var telemetry int
	if err = p.QueryRow(ctx, "SELECT count(*) FROM telemetry WHERE tenant_id='processing-retain'").Scan(&telemetry); err != nil || telemetry != 4 {
		t.Fatal("retained post-change readings lost", telemetry, err)
	}
	t.Log("actual local setupctl applied isolated staged94; exact previous-main89 API before94, current API on94, previous-main89 API on94 and current again booted with authenticated retained identity/source reads and HTTP write/retry; four rows retained; staged processing table empty/default path unchanged; not immutable images/full background/rollback restore or runtime durability acceptance")
}
