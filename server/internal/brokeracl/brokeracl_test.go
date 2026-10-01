package brokeracl

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGenerateIsDeterministicAndScoped(t *testing.T) {
	entries := []Entry{
		{Serial: "AXON-0002", TenantID: "acme", GatewayID: "gw-2"},
		{Serial: "AXON-0001", TenantID: "demo", GatewayID: "gw-1"},
	}
	a := Generate(entries)
	b := Generate([]Entry{entries[1], entries[0]}) // order-independent
	if a != b {
		t.Fatal("output depends on input order")
	}
	if !strings.Contains(a, "user AXON-0001\ntopic write t/demo/g/gw-1/telemetry\n") {
		t.Fatalf("gateway block wrong:\n%s", a)
	}
	// a gateway must never get another gateway's topics
	i1 := strings.Index(a, "user AXON-0001")
	i2 := strings.Index(a, "user AXON-0002")
	block1 := a[i1:i2]
	if strings.Contains(block1, "gw-2") || strings.Contains(block1, "acme") {
		t.Fatalf("cross-tenant leakage in block:\n%s", block1)
	}
	// command/diag reads are per-gateway
	if !strings.Contains(block1, "topic read t/demo/g/gw-1/cmd\n") {
		t.Fatalf("cmd read missing:\n%s", block1)
	}
	// no wildcards in gateway blocks
	if strings.Contains(block1, "#") && !strings.HasPrefix(block1, "#") {
		for _, line := range strings.Split(block1, "\n") {
			if strings.HasPrefix(line, "topic") && strings.Contains(line, "#") {
				t.Fatalf("wildcard in gateway block: %s", line)
			}
		}
	}
}

func TestHeaderKeepsServiceUsers(t *testing.T) {
	out := Generate(nil)
	if !strings.Contains(out, "user ingest\n") || !strings.Contains(out, "user api\n") {
		t.Fatalf("service users lost:\n%s", out)
	}
}

func TestWriteAtomic(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "acl")
	if err := WriteAtomic(p, "first"); err != nil {
		t.Fatal(err)
	}
	if err := WriteAtomic(p, "second"); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(p)
	if string(b) != "second" {
		t.Fatalf("content %q", b)
	}
	if _, err := os.Stat(p + ".tmp"); !os.IsNotExist(err) {
		t.Fatal("temp file left behind")
	}
}

// Every topic the edge uses must be granted, or mTLS deployments silently
// drop acks and manifests. Writes are own-subtree only.
func TestGenerateGrantsEdgeTopics(t *testing.T) {
	out := Generate([]Entry{{Serial: "S1", TenantID: "acme", GatewayID: "gw1"}})
	for _, want := range []string{
		"topic write t/acme/g/gw1/telemetry", "topic write t/acme/g/gw1/diag/result",
		"topic write t/acme/g/gw1/cmd/ack", "topic write t/acme/g/gw1/fleet/ack",
		"topic read t/acme/g/gw1/cmd\n", "topic read t/acme/g/gw1/fleet\n", "topic read t/acme/g/gw1/diag\n",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q", want)
		}
	}
	if strings.Contains(out, "topic write t/acme/g/gw1/cmd\n") {
		t.Error("gateway must not write its own command topic")
	}
}
