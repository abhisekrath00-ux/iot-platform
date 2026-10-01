# Function nodes (sandboxed JavaScript)

A flow graph can contain a `function` node: a short JavaScript body that
receives `msg` and returns it (changed), or returns nothing to end the path.

```js
msg.value = msg.value * 9 / 5 + 32;   // C to F
msg.vars.unit = "F";
return msg;
```

**Status: built and unit/integration tested; OFF by default.** Nothing here has
had an external security review. Treat it as a feature for trusted admins.

## Who can use it

- A tenant admin must first enable the `function_nodes` feature
  (`PUT /v1/features/function_nodes`). Until then no function node can be saved,
  imported, or run, and a stored one reports "disabled" in the run log.
- Only admins can save or import flows that contain function nodes. Operators
  cannot, even when the feature is on.
- The MCP text-to-flow tools never accept function nodes.
- Turning the feature off stops execution immediately (checked on every run).
- Enable and disable are audited (`feature.set`).

## Sandbox choice: goja

[goja](https://github.com/dop251/goja) is a pure-Go ECMAScript interpreter
(MIT-licensed). We chose it over Node's `vm` module and over a WASM runtime:

| Option | Why not / why |
|---|---|
| Node `vm` / `isolated-vm` | Needs a Node process next to the Go ingest path; `vm` is documented as not a security mechanism. |
| WASM (QuickJS in wazero) | Better memory isolation in principle (linear-memory cap). Heavier to build and measure. Reasonable next step if goja's limits prove too weak. |
| goja | One dependency, no extra process, no ambient I/O: a fresh runtime has no filesystem, network, process, timer or module access. No native memory cap (see below). |

Each run uses a new runtime. The script gets a copy of the message and returns
a value that is validated before use. A script cannot change `device_id` or
`point_id`; `value` must be a finite number; `vars` hold at most 32 entries of
string (256 bytes), number or boolean.

## Limits (all covered by tests in `server/internal/flow/jsfn`)

| Limit | Value | How |
|---|---|---|
| Wall time | 50 ms | interrupt timer. Infinite loops stop. |
| Call depth | 64 frames | runtime setting; deep recursion errors. |
| Source size | 4,000 bytes | validation on save. |
| Concurrency | 4 scripts per process | extra runs fail with "too many concurrent function runs". |
| Allocation | 64 MiB per run | watchdog samples process-wide allocation every 2 ms and interrupts. |
| Big single calls | `repeat`, `padStart`, `padEnd`, `Array.fill` capped at 100,000 | a native call cannot be interrupted, so these builtins are wrapped. |

### Known weaknesses, stated plainly

- goja has no hard memory cap. The watchdog stops a growth loop after it
  crosses 64 MiB, but one native operation can overshoot before the interrupt
  lands. In the doubling-string test the process allocated about 128 MiB before
  the stop. Other builtins that allocate in one call and are not wrapped could
  overshoot more. The wrapped list is defense in depth, not a proof.
- The allocation counter is process-wide. A very busy server can interrupt a
  legitimate script. That fails closed (the node logs an error), never open.
- The ingest process runs scripts in-process. A sandbox escape would be an
  escape into the ingest process. Run ingest with least privilege (no cloud
  credentials in its environment beyond what it needs).
- Ordinary ECMAScript is available (`Math`, `JSON`, `Date`, `RegExp`). A
  catastrophic regular expression is bounded only by the 50 ms interrupt.
- Not reviewed by an outside party; no fuzzing yet.

## Security review note

Threat: a tenant admin account is compromised, or an admin is malicious, and
uses function nodes to harm other tenants or the platform.
- Cross-tenant data: scripts receive only the message for the triggering
  reading. No database, no HTTP, no globals shared across runs.
- Denial of service: bounded by the limits above per run, 4 concurrent runs,
  and the visit limit of 500 nodes per flow run. A flow still runs once per
  matching reading, so a hot point times a slow script uses CPU: 50 ms times
  4 concurrent is the cap per process.
- Escape: no known path; none proven absent.
- Mitigations beyond code: keep the feature off unless needed, keep admins few,
  review the audit log for `feature.set` and flow publishes.
