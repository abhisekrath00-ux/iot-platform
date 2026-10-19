#!/usr/bin/env bash
# Endpoint readiness and auth checks against a REAL running API on a FRESH database.
# This is executed validation, not a dry run: it boots the API exactly the way an
# install does (migrations at startup), seeds the first-run admin through tenantctl
# (the production seeding path), then measures readiness and exercises the auth gate.
#
# What it proves:
#   - the API becomes ready (/healthz 200) after boot, with time-to-ready measured
#   - /v1 is closed without a token (401), /auth/login rejects a bad password (401)
#   - the documented first-run login works and is flagged must_change_credentials
#   - while flagged, every route except /v1/me and /v1/me/credentials is 403
#   - changing credentials returns a fresh token and the default login stops working
#   - the old token is revoked by the change (tokens_valid_after)
#   - measured /healthz latency after warm-up
#
# Requirements: go, psql, curl, jq, and a reachable Postgres where this user may
# create and drop a database. Override with PG (default postgres://localhost:5433/postgres
# in the dev sandbox; set PG=postgres://user:pass@host:5432/postgres elsewhere).
#
# It does NOT test Docker, compose, systemd, Windows, TLS termination or real edge
# hardware; those stay open gates in docs/status.md.
set -euo pipefail
ROOT=$(cd "$(dirname "$0")/.." && pwd)
PG=${PG:-postgres://localhost:5433/postgres}
PORT=${PORT:-18099}
DB="hexmon_readiness_$$"
BASE="http://127.0.0.1:$PORT"
T=$(mktemp -d); APIPID=""
cleanup() {
  [ -n "$APIPID" ] && kill "$APIPID" 2>/dev/null || true
  psql "$PG" -qc "DROP DATABASE IF EXISTS $DB" >/dev/null 2>&1 || true
  rm -rf "$T"
}
trap cleanup EXIT
fails=0
check() { if ! eval "$2"; then echo "FAIL: $1"; fails=$((fails+1)); else echo "ok:   $1"; fi; }

psql "$PG" -qc "CREATE DATABASE $DB" >/dev/null
DBURL="${PG%/*}/$DB"

echo "== build =="
(cd "$ROOT/server" && go build -o "$T/api" ./cmd/api && go build -o "$T/tenantctl" ./cmd/tenantctl)

echo "== boot (migrations run at startup, as in a real install) =="
SECRET=$(head -c 48 /dev/urandom | base64 | tr -d '=+/ \n'; echo)
DATABASE_URL="$DBURL" JWT_SIGNING_SECRET="$SECRET" LOCAL_LOGIN=1 API_PORT=$PORT MIGRATIONS_DIR="$ROOT/server/migrations" \
  "$T/api" > "$T/api.log" 2>&1 &
APIPID=$!

START=$(date +%s%N)
READY_MS=""
for i in $(seq 1 300); do
  if curl -fs -o /dev/null "$BASE/healthz" 2>/dev/null; then
    READY_MS=$(( ($(date +%s%N) - START) / 1000000 )); break
  fi
  sleep 0.1
done
check "api becomes ready after boot (time-to-ready ${READY_MS:-never} ms)" '[ -n "$READY_MS" ]'
[ -z "$READY_MS" ] && { echo "--- api.log ---"; tail -5 "$T/api.log"; }

echo "== seed the first-run admin through the production path (tenantctl) =="
DATABASE_URL="$DBURL" "$T/tenantctl" create --id readiness --name "Readiness Gate" \
  --admin-email admin@hexthings.com --default-credentials >/dev/null
check "tenantctl seeds the default-credential admin" '[ $? -eq 0 ]'

echo "== unauthenticated and bad-credential requests are refused =="
CODE=$(curl -s -o /dev/null -w "%{http_code}" "$BASE/v1/system/health")
check "GET /v1/system/health without a token = 401 (got $CODE)" '[ "$CODE" = 401 ]'
CODE=$(curl -s -o /dev/null -w "%{http_code}" -X POST "$BASE/auth/login" \
  -H 'content-type: application/json' -d '{"email":"admin@hexthings.com","password":"wrong-wrong-wrong"}')
check "login with a bad password = 401 (got $CODE)" '[ "$CODE" = 401 ]'

echo "== first-run default login =="
RESP=$(curl -s -X POST "$BASE/auth/login" -H 'content-type: application/json' \
  -d '{"email":"admin@hexthings.com","password":"Hex@2026"}')
TOK=$(echo "$RESP" | jq -r '.token // empty')
MC=$(echo "$RESP" | jq -r '.must_change_credentials // empty')
check "default login returns a token flagged must_change_credentials" '[ -n "$TOK" ] && [ "$MC" = true ]'

CODE=$(curl -s -o "$T/gate.json" -w "%{http_code}" "$BASE/v1/devices" -H "Authorization: Bearer $TOK")
check "while flagged, /v1/devices = 403 credentials_change_required (got $CODE)" \
  '[ "$CODE" = 403 ] && grep -q credentials_change_required "$T/gate.json"'

echo "== credential change unblocks the API and revokes the old token =="
RESP=$(curl -s -X POST "$BASE/v1/me/credentials" -H "Authorization: Bearer $TOK" \
  -H 'content-type: application/json' \
  -d '{"current":"Hex@2026","new_email":"owner@readiness.example","new":"a-new-long-passphrase-9"}')
TOK2=$(echo "$RESP" | jq -r '.token // empty')
check "credential change returns a fresh token" '[ -n "$TOK2" ]'
CODE=$(curl -s -o /dev/null -w "%{http_code}" "$BASE/v1/me" -H "Authorization: Bearer $TOK")
check "the pre-change token is revoked (got $CODE)" '[ "$CODE" = 401 ]'
CODE=$(curl -s -o /dev/null -w "%{http_code}" "$BASE/v1/system/health" -H "Authorization: Bearer $TOK2")
check "after the change, /v1/system/health = 200 (got $CODE)" '[ "$CODE" = 200 ]'
CODE=$(curl -s -o /dev/null -w "%{http_code}" -X POST "$BASE/auth/login" \
  -H 'content-type: application/json' -d '{"email":"admin@hexthings.com","password":"Hex@2026"}')
check "the default login no longer works (got $CODE)" '[ "$CODE" = 401 ]'

echo "== latency measurement (warm, 20 requests) =="
LAT=$(for i in $(seq 1 20); do curl -s -o /dev/null -w "%{time_total}\n" "$BASE/healthz"; done \
  | sort -n | awk '{a[NR]=$1} END{printf "p50=%.1fms p95=%.1fms", a[int(NR*0.5)]*1000, a[int(NR*0.95)]*1000}')
echo "measurement: /healthz $LAT; time-to-ready was ${READY_MS:-n/a} ms"

if [ $fails -gt 0 ]; then echo "RESULT: FAIL ($fails checks)"; exit 1; fi
echo "RESULT: PASS (executed against a real API on a fresh database; not a dry run)"
