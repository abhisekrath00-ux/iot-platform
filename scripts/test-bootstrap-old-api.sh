#!/usr/bin/env bash
# Local disposable DB/process compatibility check, never an owner bootstrap.
# No network fetch, git change, Compose action or existing deployment mutation.
# Requires TEST_DATABASE_URL pointing at a dedicated test database. The Go test
# creates and removes its own temporary schema and synthetic identity/source.
set -euo pipefail
root=$(cd "$(dirname "$0")/.." && pwd)
: "${TEST_DATABASE_URL:?point this at a dedicated isolated test database}"
old=bfe5d89b46324a566fd089b6c83ab9c111dc9231
work=$(mktemp -d /tmp/hexthings-bootstrap-old-api-XXXXXX)
trap 'rm -rf "$work"' EXIT
git -C "$root" cat-file -e "$old^{commit}"
git -C "$root" archive "$old" server | tar -x -C "$work"
(cd "$work/server" && CGO_ENABLED=0 go build -o "$work/old-api" ./cmd/api)
cd "$root/server"
HEXTHINGS_BOOTSTRAP_OLD_API="$work/old-api" HEXTHINGS_BOOTSTRAP_OLD_MIGRATIONS="$work/server/migrations" go test ./internal/setupbootstrap -run '^TestOldAPIReadsRetainedDataAfterReviewedBootstrap$' -count=1
printf '%s\n' 'PASS exact bfe5d89 old API boot and authenticated gateway/profile/device reads after four reviewed additive migrations. Not owner update, full compatibility, backup/stop-all-writers or runtime recovery proof.'
