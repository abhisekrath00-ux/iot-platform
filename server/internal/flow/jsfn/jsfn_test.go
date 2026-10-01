package jsfn

import (
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/abhisekrath00-ux/iot-platform/server/internal/flow"
)

func msg() flow.Msg {
	return flow.Msg{Value: 21.5, DeviceID: "d", PointID: "p", Vars: map[string]any{"unit": "C"}}
}

func TestTransformsMessage(t *testing.T) {
	r := New(2)
	out, drop, err := r.Run(`msg.value = msg.value * 9 / 5 + 32; msg.vars.unit = "F"; return msg;`, msg())
	if err != nil || drop {
		t.Fatal(err, drop)
	}
	if out.Value != 70.7 || out.Vars["unit"] != "F" || out.DeviceID != "d" {
		t.Fatalf("%+v", out)
	}
}

func TestReturnNothingDrops(t *testing.T) {
	_, drop, err := New(2).Run(`if (msg.value < 100) return null; return msg;`, msg())
	if err != nil || !drop {
		t.Fatal(err, drop)
	}
}

func TestScriptCannotChangeIdentityFields(t *testing.T) {
	out, _, err := New(2).Run(`msg.device_id = "other"; msg.point_id = "x"; return msg;`, msg())
	if err != nil || out.DeviceID != "d" || out.PointID != "p" {
		t.Fatalf("%+v %v", out, err)
	}
}

func TestInfiniteLoopTimesOut(t *testing.T) {
	r := New(2)
	start := time.Now()
	_, _, err := r.Run(`while(true){}`, msg())
	if err == nil || !strings.Contains(err.Error(), "stopped: timeout") {
		t.Fatal(err)
	}
	if time.Since(start) > 2*time.Second {
		t.Fatal("took too long")
	}
}

func TestDeepRecursionStopped(t *testing.T) {
	_, _, err := New(2).Run(`function f(n){return f(n+1)+1} return f(0);`, msg())
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestNoAmbientCapabilities(t *testing.T) {
	for _, code := range []string{
		`return require("fs");`, `return process.env;`, `fetch("http://x"); return msg;`,
		`setTimeout(function(){},1); return msg;`, `return XMLHttpRequest;`, `return globalThis.console.log;`,
	} {
		if _, _, err := New(2).Run(code, msg()); err == nil {
			t.Errorf("expected failure for %q", code)
		}
	}
}

func TestBadOutputsRejected(t *testing.T) {
	r := New(2)
	for _, code := range []string{
		`return 5;`, `msg.value = "abc"; return msg;`, `msg.value = NaN; return msg;`,
		`msg.vars.x = {a:1}; return msg;`, `msg.vars.x = "` + strings.Repeat("a", 300) + `"; return msg;`,
	} {
		if _, _, err := r.Run(code, msg()); err == nil {
			t.Errorf("expected rejection for %.40q", code)
		}
	}
}

func TestSyntaxCheck(t *testing.T) {
	if err := New(2).Check(`return (`); err == nil {
		t.Fatal("expected syntax error")
	}
}

func TestMemoryBombIsBoundedByTimeout(t *testing.T) {
	// Documented limitation: goja has no allocation cap. This test records
	// what a growth loop costs within the timeout, so a regression is visible.
	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	_, _, err := New(2).Run(`var a=[]; while(true){ a.push("xxxxxxxxxxxxxxxx"); }`, msg())
	runtime.ReadMemStats(&after)
	if err == nil {
		t.Fatal("expected timeout")
	}
	grown := int64(after.TotalAlloc-before.TotalAlloc) / (1 << 20)
	t.Logf("allocated during a memory bomb: %d MiB", grown)
	if grown > 512 {
		t.Fatalf("allocation within timeout too large: %d MiB", grown)
	}
}

func TestStringRepeatBomb(t *testing.T) {
	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	_, _, err := New(2).Run(`var s="x".repeat(1<<28); return msg;`, msg())
	runtime.ReadMemStats(&after)
	mib := (after.TotalAlloc - before.TotalAlloc) >> 20
	t.Logf("repeat(2^28): err=%v allocated=%d MiB", err, mib)
	if err == nil || mib > 32 {
		t.Fatalf("repeat bomb not stopped: err=%v alloc=%d MiB", err, mib)
	}
}

func TestDoublingBombStoppedByMemoryWatchdog(t *testing.T) {
	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	r := New(2)
	r.Timeout = 5 * time.Second // isolate the memory limit from the time limit
	_, _, err := r.Run(`var s="xxxxxxxx"; while(true){ s = s + s; }`, msg())
	runtime.ReadMemStats(&after)
	mib := (after.TotalAlloc - before.TotalAlloc) >> 20
	t.Logf("doubling bomb: err=%v allocated=%d MiB", err, mib)
	if err == nil || !strings.Contains(err.Error(), "memory limit") {
		t.Fatalf("expected memory limit stop, got %v", err)
	}
	if mib > 512 {
		t.Fatalf("too much allocated: %d MiB", mib)
	}
}

func TestConcurrencyLimit(t *testing.T) {
	r := New(1)
	r.sem <- struct{}{}
	if _, _, err := r.Run(`return msg;`, msg()); err == nil {
		t.Fatal("expected busy")
	}
}
