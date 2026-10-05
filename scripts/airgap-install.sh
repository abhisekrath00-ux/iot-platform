#!/usr/bin/env bash
# Installs the air-gapped bundle on a host with NO internet access.
# Usage: cd into the extracted bundle directory, then ./install.sh
set -euo pipefail
cd "$(dirname "$0")"

echo "== verifying checksums =="
sha256sum -c SHA256SUMS

echo "== loading images (this takes a few minutes) =="
gunzip -c images.tar.gz | docker load

if [ ! -f .env ]; then
  cp env.template .env
  echo "Created .env from env.template - EDIT IT NOW to set secrets, then re-run ./install.sh"
  echo "(see README-airgap.md for the air-gapped settings: internal SMTP, internal IdP, SLACK_API_BASE relay)"
  exit 0
fi

echo "== starting platform =="
# Images are already loaded, so compose will not try to build or pull.
if [ -f model.gguf ]; then
  echo "== optional local AI model found: starting ai-runtime =="
  mkdir -p models && cp model.gguf models/model.gguf
  AI_MODEL_SHA256=$(sha256sum models/model.gguf | cut -d' ' -f1) docker compose --profile ai up -d
  echo "Then in Settings, AI: base URL http://ai-runtime:8090/v1 (see README-airgap.md / ai-runtime.md)"
else
  docker compose up -d
fi
docker compose ps
echo "Done. If services show unhealthy, check 'docker compose logs' first."
