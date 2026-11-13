package setupbootstrap

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/abhisekrath00-ux/iot-platform/server/internal/auth"
	"github.com/golang-jwt/jwt/v5"
)

// The companion script builds the exact bfe5d89 source in a disposable tree.
// This optional local test is not an owner deployment or complete compatibility
// proof. It checks that the unchanged old API can boot/read retained identity,
// profiles and source config after the four exact additive migrations.
func TestOldAPIReadsRetainedDataAfterReviewedBootstrap(t *testing.T) {
	binary, oldMigrations := os.Getenv("HEXTHINGS_BOOTSTRAP_OLD_API"), os.Getenv("HEXTHINGS_BOOTSTRAP_OLD_MIGRATIONS")
	if binary == "" || oldMigrations == "" {
		t.Skip("run scripts/test-bootstrap-old-api.sh with isolated PostgreSQL")
	}
	p := isolated(t)
	applyBaseline(t, p)
	ctx := context.Background()
	if _, e := p.Exec(ctx, `INSERT INTO users(id,tenant_id,email,display_name,role) VALUES('retain-user','retain-tenant','synthetic@example.invalid','Synthetic','admin')`); e != nil {
		t.Fatal(e)
	}
	if _, e := ApplyReviewed(ctx, p, sourceDir()); e != nil {
		t.Fatal(e)
	}
	listen, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	port := listen.Addr().(*net.TCPAddr).Port
	listen.Close()
	secret := "synthetic-bootstrap-compatibility-only"
	log, e := os.CreateTemp(t.TempDir(), "old-api-log-")
	if e != nil {
		t.Fatal(e)
	}
	defer log.Close()
	dsn, e := url.Parse(p.Config().ConnString())
	if e != nil {
		t.Fatal(e)
	}
	query := dsn.Query()
	query.Set("search_path", p.Config().ConnConfig.RuntimeParams["search_path"])
	dsn.RawQuery = query.Encode()
	command := exec.Command(binary)
	command.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + t.TempDir(), "DATABASE_URL=" + dsn.String(), "MIGRATIONS_DIR=" + oldMigrations, "API_PORT=" + fmt.Sprint(port), "JWT_SIGNING_SECRET=" + secret, "MQTT_TLS=false", "MQTT_HOST=127.0.0.1", "MQTT_PORT=1", "LOCAL_LOGIN=1"}
	command.Stdout = log
	command.Stderr = log
	if e = command.Start(); e != nil {
		t.Fatal(e)
	}
	done := make(chan error, 1)
	go func() { done <- command.Wait() }()
	defer func() {
		command.Process.Signal(syscall.SIGTERM)
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			command.Process.Kill()
			<-done
		}
	}()
	client := &http.Client{Timeout: time.Second}
	base := fmt.Sprintf("http://127.0.0.1:%d", port)
	ready := false
	until := time.Now().Add(15 * time.Second)
	for time.Now().Before(until) {
		response, e := client.Get(base + "/healthz")
		if e == nil {
			io.Copy(io.Discard, response.Body)
			response.Body.Close()
			if response.StatusCode == 200 {
				ready = true
				break
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	if !ready {
		t.Fatal("old exact-checkpoint API did not become healthy after additive schema")
	}
	token, e := jwt.NewWithClaims(jwt.SigningMethodHS256, auth.Claims{TenantID: "retain-tenant", Role: "admin", RegisteredClaims: jwt.RegisteredClaims{Subject: "retain-user", ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Minute))}}).SignedString([]byte(secret))
	if e != nil {
		t.Fatal(e)
	}
	for _, check := range []struct{ path, want string }{{"/v1/gateways", "KEEP-SERIAL"}, {"/v1/profiles", "Keep source"}, {"/v1/devices", "Keep device"}} {
		req, _ := http.NewRequest("GET", base+check.path, nil)
		req.Header.Set("Authorization", "Bearer "+token)
		response, e := client.Do(req)
		if e != nil {
			t.Fatal(e)
		}
		body, e := io.ReadAll(response.Body)
		response.Body.Close()
		if e != nil || response.StatusCode != 200 || !strings.Contains(string(body), check.want) {
			t.Fatalf("old API retained-data read failed: path=%s status=%d", check.path, response.StatusCode)
		}
	}
	assertRows(t, p, 93)
}
