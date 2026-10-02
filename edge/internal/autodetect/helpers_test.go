package autodetect

import (
	"net/netip"
	"os"
	"testing"
)

func mustPrefix(t *testing.T, s string) netip.Prefix {
	p, err := netip.ParsePrefix(s)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func writeFile(p, s string) error { return os.WriteFile(p, []byte(s), 0o644) }
