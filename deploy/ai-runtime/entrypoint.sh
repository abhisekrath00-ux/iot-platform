#!/bin/sh
# Starts llama-server on loopback (never published) and the airuntime front on :8090.
# Refuses to start when MODEL_SHA256 is set and the mounted model file does not match it.
set -eu
MODEL_FILE="${MODEL_FILE:-/models/model.gguf}"
[ -r "$MODEL_FILE" ] || { echo "ai-runtime: no model at $MODEL_FILE; mount a GGUF file read-only at /models" >&2; exit 1; }
if [ -n "${MODEL_SHA256:-}" ]; then
  got=$(sha256sum "$MODEL_FILE" | cut -d' ' -f1)
  [ "$got" = "$MODEL_SHA256" ] || { echo "ai-runtime: model checksum mismatch (got $got)" >&2; exit 1; }
fi
llama-server -m "$MODEL_FILE" --host 127.0.0.1 --port 8081 -t "${AI_THREADS:-2}" -c "${AI_CONTEXT:-6144}" -np 1 --cache-ram "${AI_CACHE_RAM:-0}" --jinja &
LLAMA=$!
export MODEL_CONTEXT="${AI_CONTEXT:-6144}"
airuntime &
FRONT=$!
trap 'kill $LLAMA $FRONT 2>/dev/null' TERM INT
# either process ending ends the container, so the orchestrator restarts both
while kill -0 $LLAMA 2>/dev/null && kill -0 $FRONT 2>/dev/null; do sleep 2; done
kill $LLAMA $FRONT 2>/dev/null || true
exit 1
