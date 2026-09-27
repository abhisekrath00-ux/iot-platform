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
