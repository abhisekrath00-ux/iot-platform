package discover

import (
	"context"
	"testing"
	"time"
)

func TestScanSNMPGuards(t *testing.T) {
	if _, err := ScanSNMP(context.Background(), "8.8.8.0/24", "public", time.Second); err == nil {
		t.Error("public range accepted")
	}
	if _, err := ScanSNMP(context.Background(), "192.168.1.0/24", "", time.Second); err == nil {
		t.Error("empty community accepted")
	}
	if got := clip("a\x00b\nc"); got != "a b c" {
		t.Errorf("clip %q", got)
	}
}
