#!/usr/bin/env bash
# Report load check against a running API and its database. For a test or lab stack only: it
# writes 2.4 million synthetic rows (devices load-1..load-4, tenant TENANT) and removes them at the end.
# Needs: psql, curl. Usage: API=http://localhost:8000 TOKEN=<admin jwt> DB='postgres://...' TENANT=demo bash scripts/report-load.sh
set -euo pipefail
: "${API:?}" "${TOKEN:?}" "${DB:?}"; TENANT=${TENANT:-demo}; SECS=${SECS:-20}
H="Authorization: Bearer $TOKEN"
cleanup() { psql "$DB" -qc "SET jit=off; DELETE FROM telemetry WHERE device_id LIKE 'load-%'" >/dev/null || true; }
trap cleanup EXIT
psql "$DB" -qc "SET jit=off; INSERT INTO telemetry(event_id,tenant_id,site_id,gateway_id,device_id,point_id,observed_at,value,unit,schema_version) SELECT 'ld-'||d||'-'||g,'$TENANT',NULL,'gw-load','load-'||d,'temp',now()-(g||' seconds')::interval,20+random()*10,'C',1 FROM generate_series(1,4) d, generate_series(1,600000) g"
psql "$DB" -qc "VACUUM ANALYZE telemetry"
ID=$(curl -s -X POST "$API/v1/reports" -H "$H" -H 'Content-Type: application/json' -d '{"name":"LOADTEST 4x2.4M","definition":{"metrics":[{"point_id":"temp","device_id":"load-1"},{"point_id":"temp","device_id":"load-2"},{"point_id":"temp","device_id":"load-3"},{"point_id":"temp","device_id":"load-4"}],"group_by":"hour","window_hours":168}}' | sed -n 's/.*"id":"\([^"]*\)".*/\1/p')
[ -n "$ID" ] || { echo "could not create the test report"; exit 1; }
out=$(mktemp -d); end=$((SECONDS+SECS))
for w in 1 2; do (while [ $SECONDS -lt $end ]; do curl -s -X POST "$API/v1/reports/$ID/run" -H "$H" -o /dev/null -w '%{http_code} %{time_total}\n'; done >"$out/w$w") & done; wait
cat "$out"/w* | awk '{c[$1]++} END{for(k in c)print "status",k,c[k]}'
cat "$out"/w* | awk '$1==200{print $2}' | sort -n | awk '{a[NR]=$1} END{if(NR)print "runs",NR,"p50",a[int(NR/2)+1],"p95",a[int(NR*0.95)+1],"max",a[NR]}'
psql "$DB" -qc "DELETE FROM reports WHERE name LIKE 'LOADTEST%'" >/dev/null
rm -rf "$out"
