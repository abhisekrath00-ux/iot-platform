#!/usr/bin/env bash
# Builds an air-gapped install bundle: every platform and third-party image
# (including optional-profile services like Redis) as a single tarball, plus
# compose file, env template, install script, and checksums.
# Run on a machine WITH internet, then copy dist/airgap/ to the target.
set -euo pipefail
cd "$(dirname "$0")/.."
OUT=dist/airgap
rm -rf "$OUT"; mkdir -p "$OUT"

# --profile ha makes optional services (redis) visible to config/pull so the
# bundle supports HA installs offline too.
COMPOSE="docker compose --profile ha"

echo "== building platform images =="
$COMPOSE build api ingest mcp web
# edge agent image is built separately: it deploys to gateways, not the core host
docker build -t hexmon/edge-agent:latest ./edge

echo "== pulling third-party images =="
$COMPOSE pull postgres mosquitto elasticsearch redis

IMAGES=$($COMPOSE config --images)
IMAGES="$IMAGES hexmon/edge-agent:latest"
echo "== saving images =="
echo "$IMAGES" | tr ' ' '\n' | sed 's/^/  /'
docker save $IMAGES | gzip > "$OUT/images.tar.gz"

cp docker-compose.yml "$OUT/"
if [ -f .env.example ]; then
  cp .env.example "$OUT/env.template"
else
  echo "FATAL: .env.example missing - bundle would have no env template" >&2
  exit 1
fi
cp scripts/airgap-install.sh "$OUT/install.sh"
chmod +x "$OUT/install.sh"
cp docs/airgap.md "$OUT/README-airgap.md"
(cd "$OUT" && sha256sum * > SHA256SUMS)
echo "bundle ready in $OUT"
du -sh "$OUT"
