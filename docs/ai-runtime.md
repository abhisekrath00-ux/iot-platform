# AI runtime: the bundled local model

The platform works without it. When an admin connects a model (Settings, AI), the chat panel and
the assistant use it. The bundled option runs a small model on the same host, fully offline.

## What runs

```
browser -> web -> API (/v1/assistant/chat?stream=1) -> ai-runtime:8090 (airuntime) -> llama-server:8081 (loopback only)
```

- `llama-server` is llama.cpp, CPU only, built from a pinned commit (`deploy/ai-runtime/Dockerfile`).
- `airuntime` (`server/cmd/airuntime`) is a small Go front. It serves `/health`, `/ready`, `/version`,
  `/model`, `/metrics`, requires a bearer key on the model API when `AI_RUNTIME_KEY` is set, exposes only
  `/v1/chat/completions` and `/v1/models`, refuses bodies over 1 MiB, and counts requests and latency.
- The orchestrator, tool policy, approval cards and audit stay in the API. The runtime holds no platform
  data or credentials, and the model never gets a shell, a database, the filesystem or the network.
- The model file is not in the image. Mount it read-only at `/models/model.gguf`. Set `AI_MODEL_SHA256`
  and the container refuses to start on a mismatch.

## Start it

```
mkdir models && cp Qwen3-1.7B-Q4_K_M.gguf models/model.gguf
AI_MODEL_SHA256=$(sha256sum models/model.gguf | cut -d' ' -f1) docker compose --profile ai up -d ai-runtime
```

Then in Settings, AI: base URL `http://ai-runtime:8090/v1`, model `qwen3-1.7b`, and the key if you set one.
Raise `AI_TIMEOUT_SECONDS` on the API (default 90, max 600) on slow CPUs. Nothing is published on the host.

## Air-gapped bundle

`AI_MODEL_FILE=/path/model.gguf scripts/airgap-bundle.sh` adds the ai-runtime image and the model to the
bundle; `install.sh` starts the `ai` profile when `model.gguf` is present. Without the variable the bundle
has no AI. **Status: scripts pass `bash -n` only. They were never run (no Docker on the dev machine).**

## Model choice

The default is Qwen3-1.7B Q4_K_M (Qwen3 has no 1.5B). Any GGUF that llama.cpp supports and that handles
tool calls works by changing the mounted file and `AI_MODEL_NAME`. Hosted endpoints work too through the
same settings page; nothing here needs the internet at run time.

## Measured, on the dev machine only (2 GB RAM, 2 CPUs, no GPU, other services running)

| Measure | Value |
|---|---|
| Model file | 1.1 GB (sha256 b139949c...1897) |
| Resident memory with `-c 3072` | about 1.1 GB |
| Load time | about 6 s warm |
| Prompt processing | about 50 tokens/s |
| Generation | about 1 token/s when the machine is short of memory (page cache pressure), not representative of a proper host |
| First answer of a short chat through the API | tens of seconds (the system prompt is about 1,000 tokens) |

These numbers say little about a production host. The 4-5 GB budget in the brief has not been measured
because this machine cannot give it. Building the Go API while the model is loaded makes the machine
thrash; stop the model first.

## What is tested and what is not

- Tested: the airuntime front against a fake llama-server (health states, key, allow-list, size cap,
  metrics); the streaming client and the agent loop's events against a scripted event-stream server;
  the status probe states; page-context sanitising; the markdown renderer.
- Run for real: llama-server with Qwen3-1.7B behind airuntime and the API, one streamed answer.
- Not done: building or running the container image; any measurement on a proper host; quality of the
  model's answers across a task set (the eval suite is phase 9).
- Observed limit: with the generic `api_request` tool the 1.7B model produced a malformed path on a simple
  question. The policy refused it. Typed tools (phase 2) are the fix, not a bigger prompt.

## Security notes

- The runtime key is checked in constant time and never forwarded to llama-server.
- The status endpoint reports state and model name only, never the address or key.
- Page context is a short path of safe characters; it cannot carry instructions.
- Streamed answers are rendered as text elements, never as HTML.
