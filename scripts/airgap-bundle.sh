#!/usr/bin/env bash
# Builds an air-gapped install bundle: all platform and third-party images
# as a single tarball plus compose file, env template, and checksums.
# Run on a machine WITH internet, then copy dist/airgap/ to the target.
set -euo pipefail
cd "$(dirname "$0")/.."
OUT=dist/airgap
rm -rf "$OUT"; mkdir -p "$OUT"

echo "== building platform images =="
docker compose build api ingest mcp web
# edge agent image is built separately: it deploys to gateways, not the core host
docker build -t hexmon/edge-agent:latest ./edge

echo "== pulling third-party images =="
docker compose pull postgres mosquitto elasticsearch

IMAGES=$(docker compose config --images)
IMAGES="$IMAGES hexmon/edge-agent:latest"
echo "== saving images =="
docker save $IMAGES | gzip > "$OUT/images.tar.gz"
cp docker-compose.yml "$OUT/"
cp .env.example "$OUT/env.template" 2>/dev/null || cp deploy/env.template "$OUT/env.template" 2>/dev/null || true
cp docs/airgap.md "$OUT/README-airgap.md"
(cd "$OUT" && sha256sum * > SHA256SUMS)
echo "bundle ready in $OUT"
du -sh "$OUT"
