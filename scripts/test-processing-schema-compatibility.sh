#!/usr/bin/env bash
# Isolated local processes/DB only. No owner service, image switch or deployment.
set -euo pipefail
root=$(cd "$(dirname "$0")/.." && pwd)
: "${TEST_DATABASE_URL:?point at isolated PostgreSQL with create-database rights}"
work=$(mktemp -d /tmp/hexthings-processing-compat-XXXXXX)
trap 'rm -rf "$work"' EXIT
old=bfe5d89b46324a566fd089b6c83ab9c111dc9231
git -C "$root" archive "$old" server | tar -x -C "$work"
(cd "$work/server" && CGO_ENABLED=0 go build -o "$work/previous-api" ./cmd/api)
(cd "$root/server" && CGO_ENABLED=0 go build -o "$work/current-api" ./cmd/api && CGO_ENABLED=0 go build -o "$work/setupctl" ./cmd/setupctl)
cd "$root/server"
TEST_PROCESSING_PREVIOUS_API="$work/previous-api" TEST_PROCESSING_CURRENT_API="$work/current-api" TEST_PROCESSING_SETUPCTL="$work/setupctl" TEST_PROCESSING_PREVIOUS_MIGRATIONS="$work/server/migrations" go test -race ./internal/processingschema -run '^TestStagedPreviousCurrentPreviousAPIsAndOperator$' -count=1 -v
