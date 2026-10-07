package leader

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Helper mode: this test binary re-executes itself as a separate OS process that contends for
// the lock and, while leader, touches <dir>/<name> every 40ms.
func TestMain(m *testing.M) {
	if name := os.Getenv("LEADER_HELPER_NAME"); name != "" {
		pool, err := pgxpool.New(context.Background(), os.Getenv("TEST_DATABASE_URL"))
		if err != nil {
			os.Exit(2)
		}
		file := filepath.Join(os.Getenv("LEADER_HELPER_DIR"), name)
		Run(context.Background(), pool, 555000111, name, 50*time.Millisecond, func(ctx context.Context) {
			for ctx.Err() == nil {
				os.WriteFile(file, []byte(strconv.FormatInt(time.Now().UnixNano(), 10)), 0o644)
				time.Sleep(40 * time.Millisecond)
			}
		})
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func fresh(dir, name string) bool {
	b, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		return false
	}
	ns, err := strconv.ParseInt(string(b), 10, 64)
	return err == nil && time.Since(time.Unix(0, ns)) < 300*time.Millisecond
}

// Two real processes, one lock: exactly one runs the job; SIGKILL of the leader (no clean unlock)
// hands the job to the other process; the two are never both active during sampling.
func TestTwoProcessesAndKillFailover(t *testing.T) {
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	dir := t.TempDir()
	start := func(name string) *exec.Cmd {
		c := exec.Command(os.Args[0], "-test.run=NONE")
		c.Env = append(os.Environ(), "LEADER_HELPER_NAME="+name, "LEADER_HELPER_DIR="+dir)
		if err := c.Start(); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { c.Process.Kill(); c.Wait() })
		return c
	}
	procs := map[string]*exec.Cmd{"p1": start("p1"), "p2": start("p2")}

	var leaderName string
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) && leaderName == "" {
		time.Sleep(100 * time.Millisecond)
		a, b := fresh(dir, "p1"), fresh(dir, "p2")
		if a && b {
			t.Fatal("both processes running the job")
		}
		if a {
			leaderName = "p1"
		} else if b {
			leaderName = "p2"
		}
	}
	if leaderName == "" {
		t.Fatal("no leader emerged in 10s")
	}
	for i := 0; i < 20; i++ { // steady state: only the leader is active
		time.Sleep(50 * time.Millisecond)
		if fresh(dir, "p1") && fresh(dir, "p2") {
			t.Fatal("both active in steady state")
		}
	}
	other := "p1"
	if leaderName == "p1" {
		other = "p2"
	}
	killedAt := time.Now()
	procs[leaderName].Process.Kill()
	procs[leaderName].Wait()
	took := time.Duration(0)
	for time.Now().Before(killedAt.Add(15 * time.Second)) {
		time.Sleep(50 * time.Millisecond)
		if fresh(dir, other) {
			took = time.Since(killedAt)
			break
		}
	}
	if took == 0 {
		t.Fatalf("survivor %s never took over after SIGKILL of %s", other, leaderName)
	}
	t.Logf("leader %s killed; %s took over in %v", leaderName, other, took)
}
