package main

import (
	"os"
	"regexp"
	"testing"

	"github.com/abhisekrath00-ux/iot-platform/server/internal/store"
)

// Regression: leader jobs each pin a connection. If they ever outnumber the
// minimum pool, requests starve and the API hangs.
func TestLeaderJobsFitInPool(t *testing.T) {
	src, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatal(err)
	}
	n := len(regexp.MustCompile(`leader\.Run\(`).FindAll(src, -1))
	if n == 0 || int32(n)+8 > store.MinPoolSize {
		t.Fatalf("%d leader jobs vs MinPoolSize %d: keep at least 8 connections free for requests", n, store.MinPoolSize)
	}
}
