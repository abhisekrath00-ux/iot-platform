# MCP eval suite

Two layers keep the MCP gateway safe for AI clients:

1. **Offline contract tests** (`go test ./cmd/mcp/`) - JSON-RPC plumbing,
   the read-only tool allowlist (asserts no write-shaped tool ever ships),
   unknown-tool/method errors, notification acks. Runs in CI on every push.
2. **Live golden replay** (`cmd/mcpeval`) - replays `golden.json` against a
   deployed stack with a delegated JWT and asserts the answers: site
   inventory, device health shape, the 24h range clamp, clean error paths,
   and that the allowlist contains nothing that can actuate.

## Running the live suite

    # against a compose stack (demo tenant seeded by default):
    go run ./cmd/mcpeval -url http://localhost:8100 -token "$JWT"

Exit code 1 = at least one case failed. Add a case to
`server/cmd/mcpeval/golden.json` whenever a tool is added or its contract
changes; the `question` field records the natural-language intent so the
set doubles as client-integration documentation.

Air-gap note: both layers run fully offline - no external model or network
service is involved.

## Tool catalog (all read-only, tenant-scoped, bounded)

| Tool | Returns | Bounds |
|------|---------|--------|
| `list_sites` | sites with gateway/device counts | - |
| `list_devices` | devices with profile and gateway | 200 rows |
| `get_device_health` | latest reading and age per point | one device |
| `query_time_series` | raw readings for a point | 24 h, 500 points |
| `aggregate_time_series` | bucketed avg/min/max/sum/count | 7 days, 500 buckets (rejected beyond) |
| `list_alerts` | recent alerts, optional status filter | 100 rows, status validated |
| `explain_alert` | alert details | one alert |

No tool can write or actuate. `cmd/mcp/db_test.go` checks tenant isolation, status validation and aggregation math against real Postgres (CI runs it with `TEST_DATABASE_URL`).
