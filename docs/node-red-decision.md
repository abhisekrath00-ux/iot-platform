# Node-RED: embed or build our own executor?

Request (relayed from the product owner, Oct 1 10:36 PM): build on Node-RED and get its functionality, with an MCP that turns text into Node-RED-style graphs.

## Facts checked

- Node-RED is Apache License 2.0, copyright OpenJS Foundation and other
  contributors (https://github.com/node-red/node-red/blob/HEAD/LICENSE). That
  permits embedding and redistribution with the licence and notices kept.
- Node-RED documents embedding it in an Express app
  (https://nodered.org/docs/user-guide/runtime/embedding). When embedded,
  `httpAdminAuth`, `httpNodeAuth` and several other settings are left to the
  host application.
- By default the editor is unsecured: anyone who can reach it can deploy
  changes (https://nodered.org/docs/user-guide/runtime/securing-node-red). Its
  built-in auth defines a small fixed set of users per runtime.
- Palette nodes each carry their own licence. Not audited here; any bundled
  palette must be reviewed node by node.

## What this means for a multi-tenant product

A Node-RED runtime is one flow set with one set of credentials and one global
context. It has no tenant concept. Its function node and many palette nodes run
arbitrary code with network and filesystem access (http request, exec, file).
So one tenant per runtime is the only safe shape, which means:

| | Sidecar per tenant | Our own executor (chosen) |
|---|---|---|
| Isolation | Process or container per tenant; strong if configured well | Shared process; no I/O nodes exist; function node sandboxed and off by default |
| Features | Whole palette, real editor | Only the node types we build |
| Air-gap | Palette installs need an offline npm mirror | Nothing extra |
| Ops cost | A Node runtime per tenant, upgrades, CVEs in palette nodes | One Go binary |
| Safety gating | Node-RED can call any network or device, bypassing approvals and four-eyes | Flows can only notify; no control path |

The last row decides it. Physical control must go through approval and
four-eyes (docs/security.md). An embedded Node-RED with MQTT-out or HTTP-out
nodes is a path around that.

## Decision

Built: our own executor, Node-RED-style (graph, switch, change, delay, debug,
function) plus Node-RED JSON import/export for the supported subset, plus MCP
text-to-draft tools.

Not built: the real Node-RED runtime or its palette. If a customer needs it, the
supportable shape is a dedicated Node-RED container per tenant, on a separate
network, fed from our webhooks and API, with no route to the control path.
That is a deployment recipe, not shipped code, and it needs its own review.

The claim "all Node-RED functionality" is therefore not met. The gap table in
competitive-gap.md says so.
