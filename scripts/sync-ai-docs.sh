#!/bin/sh
# Copies docs/*.md into the corpus the API embeds for the assistant's documentation search.
# Run after editing docs; a test fails when the copy is stale.
set -eu
cd "$(dirname "$0")/.."
dst=server/internal/docsrag/corpus
mkdir -p "$dst"
rm -f "$dst"/*.md
cp docs/*.md "$dst"/
echo "synced $(ls "$dst" | wc -l) docs"
