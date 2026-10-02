package notify

import (
	"context"
	"net"
	"strings"
	"testing"
)

func TestParseKafkaTarget(t *testing.T) {
	b, topic, err := ParseKafkaTarget("kafka://k1:9092,k2:9093/alerts")
	if err != nil || len(b) != 2 || b[1] != "k2:9093" || topic != "alerts" {
		t.Fatalf("%v %v %v", b, topic, err)
	}
	for _, bad := range []string{"", "http://k:9092/t", "kafka://k:9092", "kafka://k/t", "kafka://u:p@k:9092/t", "kafka://k:9092/a/b", "kafka://k:9092/a b"} {
		if _, _, err := ParseKafkaTarget(bad); err == nil {
			t.Errorf("accepted %q", bad)
		}
	}
}

func TestParseAMQPTarget(t *testing.T) {
	a, v, ex, k, err := ParseAMQPTarget("amqp://mq.local/prod?exchange=iot&key=alerts")
	if err != nil || a != "mq.local:5672" || v != "/prod" || ex != "iot" || k != "alerts" {
		t.Fatalf("%v %v %v %v %v", a, v, ex, k, err)
	}
	for _, bad := range []string{"", "amqps://h/x?key=k", "amqp://u:p@h/x?key=k", "amqp://h/x", "amqp://:5672/x?key=k"} {
		if _, _, _, _, err := ParseAMQPTarget(bad); err == nil {
			t.Errorf("accepted %q", bad)
		}
	}
}

// The dial guard must stop a bus target from reaching loopback/metadata
// addresses, exactly like the webhook client.
func TestBusDialerBlocksInternalAddresses(t *testing.T) {
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	defer ln.Close()
	n := &Notifier{}
	if _, err := n.dialer().DialContext(context.Background(), "tcp", ln.Addr().String()); err == nil || !strings.Contains(err.Error(), "not allowed") {
		t.Fatalf("loopback dial allowed: %v", err)
	}
	if _, err := n.dialer().DialContext(context.Background(), "tcp", "169.254.169.254:80"); err == nil || !strings.Contains(err.Error(), "not allowed") {
		t.Fatalf("metadata dial allowed: %v", err)
	}
	if err := n.Kafka(context.Background(), "kafka://127.0.0.1:1/t", "x", nil); err == nil {
		t.Fatal("kafka to loopback succeeded")
	}
	t.Setenv("AMQP_USERNAME", "")
	if err := n.AMQP(context.Background(), "amqp://127.0.0.1/v?key=k", "x", nil); err == nil || !strings.Contains(err.Error(), "not configured") {
		t.Fatalf("amqp without creds: %v", err)
	}
}
