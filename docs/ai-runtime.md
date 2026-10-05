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

## What "trained on the software" means here

No model is trained or fine-tuned on HexThings. The assistant is a stock Qwen3-1.7B. It knows the product
through tools (read-only queries that really exist) and a search over the shipped docs (docsrag).
Deterministic code does the facts and arithmetic; the model only phrases the answer. A 11-of-120 sample
eval scored 6 right, so expect a small model to make mistakes.

## Installer download

`install.sh --ai-download` and `install.ps1 -AiDownload` (the one-command install does this by default;
`HEXTHINGS_NO_AI=1` skips it) fetch the model once from Hugging Face, resume if interrupted, check the
sha256 (a mismatch deletes the file), then start the `ai` profile and run
`tenantctl ai-connect` so the assistant is connected without manual settings. For air-gapped hosts copy
`models/model.gguf` in beforehand, or use the airgap bundle.

## Model choice

The default is Qwen3-1.7B Q4_K_M (Qwen3 has no 1.5B). Any GGUF that llama.cpp supports and that handles
tool calls works by changing the mounted file and `AI_MODEL_NAME`. Hosted endpoints work too through the
same settings page; nothing here needs the internet at run time.

## Measured, on the dev machine only (2 GB RAM, 2 CPUs, no GPU, other services running)

| Measure | Value |
|---|---|
| Model file | 1.1 GB (sha256 b139949c...1897) |
| Resident memory with `-c 6144` | about 1.5 GB peak measured after three real requests (full KV cache adds up to about 0.7 GB) |
| Load time | about 6 s warm |
| Prompt processing | about 50 tokens/s |
| Generation | about 1 token/s when the machine is short of memory (page cache pressure), not representative of a proper host |
| First answer of a short chat through the API | tens of seconds (the system prompt is about 1,000 tokens) |

### Memory and CPU-only (re-measured Oct 5 with the real pinned llama.cpp build and the real model)

llama-server (commit d89651a, CPU only, `-t 2 -c 3072 --jinja`), real Qwen3-1.7B Q4_K_M, sha256 verified
against the download: **resident memory 1.45 GB after load, 1.49 GB peak after a chat request**. A short
reply ran at about 7 tokens/s generation and 27 tokens/s prompt processing on 2 CPUs, no GPU. This is the
model process measured outside Docker. The compose `ai-runtime` limit is now 3 GB (`AI_MEM_LIMIT`).

The whole stack inside 4-5 GB is an **estimate, not a measurement**: model about 1.5 GB, Elasticsearch heap
512 MB (about 1 GB resident), Postgres, API, web and the other services about 1-1.5 GB. That is roughly
4 GB. Docker was not available here, so the full stack was never run together. On a 4 GB machine use
`--no-ai` or a smaller context (`AI_CONTEXT=2048`).

The full 4-5 GB figure for the stack and the Docker build of the llama.cpp image remain unmeasured. Building the Go API while the model is loaded makes the machine
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

## Context window and small-model prompts

The default context is now 6144 tokens (`AI_CONTEXT`). A plain "hey" costs 836 prompt tokens, a create-site request 1343 and an alerts question 1309, measured with the real Qwen3-1.7B (direct requests to llama-server, 1.52 GB peak RSS; not the full API path, Docker not run). The system prompt is compact and only about 9 keyword-relevant tools are offered per turn. If the model still reports a context overflow, the API retries once with just the latest message, then shows a friendly message suggesting Clear or a larger `AI_CONTEXT`.
