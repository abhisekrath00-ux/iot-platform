package redisx

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestCachePreflightContract(t *testing.T) {
	replies := []string{"PONG", "OK", "probe", "OK", "1", "1"}
	for fail := -1; fail < len(replies); fail++ {
		for _, wrongReply := range []bool{false, true} {
			count := 0
			err := checkCachePermissions(context.Background(), func(ctx context.Context, args ...string) (string, error) {
				step := count
				count++
				if step == fail {
					if wrongReply {
						return "unexpected secret", nil
					}
					return "", errors.New("endpoint password secret")
				}
				if step == 1 || step == 3 {
					if len(args) != 6 || args[3] != "EX" || args[4] != "30" || args[5] != "NX" {
						t.Fatalf("unsafe seed: %v", args)
					}
				}
				if step > 0 && !strings.HasPrefix(args[1], "hx:") {
					t.Fatal("unscoped key")
				}
				if step == 4 && args[0] != "INCR" {
					t.Fatal("missing generation permission probe")
				}
				return replies[step], nil
			}, "test-nonce")
			if fail < 0 {
				if err != nil || count != 6 {
					t.Fatal(err, count)
				}
			} else if err == nil || count != fail+1 || strings.Contains(err.Error(), "secret") {
				t.Fatal("failure not bounded/sanitized", err, count)
			}
		}
	}
}
