#!/usr/bin/env bash
# Explicitly isolated local logical backup proof. Never points at an owner DB.
set -euo pipefail
root=$(cd "$(dirname "$0")/.." && pwd)
: "${TEST_DATABASE_URL:?isolated PostgreSQL with create-database rights required}"
work=$(mktemp -d /tmp/hexthings-denial-restore-XXXXXX)
trap 'rm -rf "$work"' EXIT
dump=${TEST_DENIAL_PG_DUMP:-$(command -v pg_dump)}
restore=${TEST_DENIAL_PG_RESTORE:-$(command -v pg_restore)}
(cd "$root/server" && CGO_ENABLED=0 go build -o "$work/setupctl" ./cmd/setupctl)
cd "$root/server"
TEST_DENIAL_SETUPCTL="$work/setupctl" TEST_DENIAL_PG_DUMP="$dump" TEST_DENIAL_PG_RESTORE="$restore" go test -race ./internal/ingestprocessing -run '^(TestDenialActualOperatorLogicalBackupRestore|TestRuntimeActualKilledBatchLogicalRestoreWithoutResend)$' -count=1 -v
