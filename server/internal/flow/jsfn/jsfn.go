// Package jsfn runs flow "function" nodes in a goja sandbox.
//
// goja is a pure-Go ECMAScript interpreter. A fresh runtime has no filesystem,
// network, process, timer or module access; the only data in is a copy of the
// message and the only data out is a validated copy of it. Limits enforced
// here: wall-clock timeout (interrupt), call-stack depth, source size, output
// shape. goja has no allocation cap: see docs/function-nodes.md for what that
// means and how it is bounded.
package jsfn

import (
	"fmt"
	"math"
	"runtime/metrics"
	"sync"
	"time"

	"github.com/abhisekrath00-ux/iot-platform/server/internal/flow"
	"github.com/dop251/goja"
)

// MaxAllocBytes bounds bytes allocated process-wide while one script runs. A
// watchdog samples the runtime and interrupts the script when it is exceeded.
// It is process-wide, so a very busy server can interrupt a legitimate script:
// that fails closed (the node reports an error), never open.
const MaxAllocBytes = 64 << 20

// prelude caps the builtins that can allocate a huge value in one native call
// (a single call cannot be interrupted until it returns).
const prelude = `(function(){
var cap = 100000;
var rep = String.prototype.repeat, ps = String.prototype.padStart, pe = String.prototype.padEnd;
String.prototype.repeat = function(n){ if (this.length * n > cap) throw new RangeError("repeat too large"); return rep.call(this, n); };
String.prototype.padStart = function(n, f){ if (n > cap) throw new RangeError("pad too large"); return ps.call(this, n, f); };
String.prototype.padEnd = function(n, f){ if (n > cap) throw new RangeError("pad too large"); return pe.call(this, n, f); };
var A = Array, fill = A.prototype.fill;
A.prototype.fill = function(){ if (this.length > cap) throw new RangeError("array too large"); return fill.apply(this, arguments); };
})();`

var preludeProg = goja.MustCompile("prelude", prelude, true)

func allocated() uint64 {
	s := []metrics.Sample{{Name: "/gc/heap/allocs:bytes"}}
	metrics.Read(s)
	if s[0].Value.Kind() == metrics.KindUint64 {
		return s[0].Value.Uint64()
	}
	return 0
}

const (
	DefaultTimeout = 50 * time.Millisecond
	maxStack       = 64
	maxStr         = 256
	maxVars        = 32
)

type Runner struct {
	Timeout time.Duration
	mu      sync.Mutex
	cache   map[string]*goja.Program
	sem     chan struct{}
}

// New returns a runner allowing at most maxConcurrent scripts at once.
func New(maxConcurrent int) *Runner {
	if maxConcurrent < 1 {
		maxConcurrent = 4
	}
	return &Runner{Timeout: DefaultTimeout, cache: map[string]*goja.Program{}, sem: make(chan struct{}, maxConcurrent)}
}

func (r *Runner) program(code string) (*goja.Program, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if p, ok := r.cache[code]; ok {
		return p, nil
	}
	p, err := goja.Compile("function-node", "(function(msg){\n"+code+"\n})", true)
	if err != nil {
		return nil, fmt.Errorf("syntax: %v", err)
	}
	if len(r.cache) >= 256 {
		r.cache = map[string]*goja.Program{}
	}
	r.cache[code] = p
	return p, nil
}

// Check compiles code without running it (used when a flow is saved).
func (r *Runner) Check(code string) error { _, err := r.program(code); return err }

func (r *Runner) Run(code string, in flow.Msg) (flow.Msg, bool, error) {
	select {
	case r.sem <- struct{}{}:
		defer func() { <-r.sem }()
	default:
		return in, false, fmt.Errorf("too many concurrent function runs")
	}
	prog, err := r.program(code)
	if err != nil {
		return in, false, err
	}
	vm := goja.New()
	vm.SetMaxCallStackSize(maxStack)
	if _, err := vm.RunProgram(preludeProg); err != nil {
		return in, false, fmt.Errorf("sandbox init: %v", err)
	}
	timer := time.AfterFunc(r.Timeout, func() { vm.Interrupt("timeout") })
	defer timer.Stop()
	stop := make(chan struct{})
	defer close(stop)
	base := allocated()
	go func() {
		t := time.NewTicker(2 * time.Millisecond)
		defer t.Stop()
		for {
			select {
			case <-stop:
				return
			case <-t.C:
				if allocated()-base > MaxAllocBytes {
					vm.Interrupt("memory limit")
					return
				}
			}
		}
	}()

	vars := map[string]any{}
	for k, v := range in.Vars {
		vars[k] = v
	}
	msg := map[string]any{"value": in.Value, "device_id": in.DeviceID, "point_id": in.PointID, "vars": vars}
	var out any
	err = func() (err error) {
		defer func() {
			if rec := recover(); rec != nil {
				err = fmt.Errorf("script panic: %v", rec)
			}
		}()
		fv, err := vm.RunProgram(prog)
		if err != nil {
			return err
		}
		fn, ok := goja.AssertFunction(fv)
		if !ok {
			return fmt.Errorf("internal: not a function")
		}
		res, err := fn(goja.Undefined(), vm.ToValue(msg))
		if err != nil {
			return err
		}
		if goja.IsUndefined(res) || goja.IsNull(res) {
			out = nil
			return nil
		}
		out = res.Export()
		return nil
	}()
	if err != nil {
		if ie, ok := err.(*goja.InterruptedError); ok {
			return in, false, fmt.Errorf("stopped: %v (limits: %v, %d MiB allocated)", ie.Value(), r.Timeout, MaxAllocBytes>>20)
		}
		return in, false, fmt.Errorf("%v", err)
	}
	if out == nil {
		return in, true, nil
	}
	m, ok := out.(map[string]any)
	if !ok {
		return in, false, fmt.Errorf("function must return the msg object or nothing")
	}
	return sanitize(m, in)
}

// sanitize turns whatever the script returned into a valid Msg.
func sanitize(m map[string]any, in flow.Msg) (flow.Msg, bool, error) {
	out := flow.Msg{DeviceID: in.DeviceID, PointID: in.PointID, Vars: map[string]any{}}
	switch v := m["value"].(type) {
	case float64:
		out.Value = v
	case int64:
		out.Value = float64(v)
	default:
		return in, false, fmt.Errorf("msg.value must be a number")
	}
	if math.IsNaN(out.Value) || math.IsInf(out.Value, 0) {
		return in, false, fmt.Errorf("msg.value must be finite")
	}
	if vs, ok := m["vars"].(map[string]any); ok {
		if len(vs) > maxVars {
			return in, false, fmt.Errorf("too many vars")
		}
		for k, v := range vs {
			if len(k) == 0 || len(k) > 32 {
				return in, false, fmt.Errorf("bad var name")
			}
			switch x := v.(type) {
			case string:
				if len(x) > maxStr {
					return in, false, fmt.Errorf("var %s too long", k)
				}
				out.Vars[k] = x
			case float64:
				if math.IsNaN(x) || math.IsInf(x, 0) {
					return in, false, fmt.Errorf("var %s not finite", k)
				}
				out.Vars[k] = x
			case int64:
				out.Vars[k] = float64(x)
			case bool:
				out.Vars[k] = x
			default:
				return in, false, fmt.Errorf("var %s must be string, number or boolean", k)
			}
		}
	}
	return out, false, nil
}
