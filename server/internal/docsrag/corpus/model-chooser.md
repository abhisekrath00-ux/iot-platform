# Local AI model chooser

AI is optional. The terminal lists five real open-license model artifacts and a
`skip` choice before any model download. Listing and recommendation use no network.
All downloads need explicit choice; unattended one-command setup no longer pulls
AI automatically. `HEXTHINGS_NO_AI=1` skips AI; `HEXTHINGS_AI_MODEL_SIZE=qwen3-1.7b`
is an explicit choice for the one-command bootstrap.

## Choose during install or later

- Linux/macOS/WSL: `bash scripts/install.sh --ai-model-size qwen3-1.7b`.
- Windows: `powershell -ExecutionPolicy Bypass -File scripts\install.ps1 -AiModelSize qwen3-1.7b`.
- Windows menu: `hexthings` > Local AI > Choose model size.
- Windows command: `hexthings ai models` to list; `hexthings ai choose -ModelSize qwen3-4b` to change.
- Linux later: `bash scripts/model-choose.sh --list`, then `bash scripts/model-choose.sh --size qwen3-4b`.
- `--no-ai` / `-NoAi` skips the installer's AI profile. `--ai-model FILE` / `-AiModel FILE`
  accepts an offline GGUF. The chooser's `skip` keeps any existing model and does not
  stop a running runtime. Use `hexthings ai stop` or `docker compose --profile ai stop ai-runtime` to stop it.

The table shows detected host RAM, logical CPU count, free disk, decimal GB download,
license, test status, conservative whole-stack RAM estimates and a verdict.
Docker Desktop/WSL may have less RAM than the host: check its own limit before choosing.
CPU count is not a speed prediction. FIT is not a performance guarantee.

| Choice | Artifact download | Whole-stack min / comfortable RAM (MiB) | Execution status |
| --- | ---: | ---: | --- |
| Qwen3 1.7B Q4_K_M | 1,107,409,472 bytes (1.11 GB) | 4096 / 5120 | Earlier measurement here only: 1.49 GB peak model RSS, about 7 tokens/s on 2 CPU cores. Not a whole-stack or Windows benchmark. |
| Qwen3 4B Q4_K_M | 2,497,281,312 bytes (2.50 GB) | 6144 / 8192 | Never run here. |
| Qwen3 8B Q4_K_M | 5,027,784,512 bytes (5.03 GB) | 9216 / 12288 | Never run here. |
| gpt-oss 20B Q4_K_M | 11,624,759,488 bytes (11.62 GB) | 16384 / 20480 | Never run here; runtime compatibility unverified. MoE, about 3.6B active parameters. |
| gpt-oss 120B Q4_K_M | 62,768,723,552 bytes (62.77 GB), two parts | 71680 / 81920 | Manual only: split GGUF handling and runtime compatibility unverified. MoE, about 5.1B active parameters. |

These are the actual available 4B/8B options, not nonexistent 5B/10B variants.
The recommendation is the measured 1.7B model only when comfortably within the
estimates; otherwise skip. A 4 GiB host defaults to skip. Large models are not
recommended just because a host has enough RAM.

TIGHT or TOO-BIG requires `--force` / `-Force` (`--ai-force` / `-AiForce` during
install). That override accepts only estimated RAM risk. Insufficient disk,
checksum errors, air-gapped bundles and `HEXTHINGS_SKIP_DOWNLOAD=1` still block.
Disk reserves 10 GiB for the platform as well as the download. Windows detection
uses CIM and DriveInfo; resource detection has not been tested on a real Windows host.

## Verification and replacement

`scripts/model-catalog.txt` is the shared source for both shell and PowerShell.
Byte sizes and SHA-256 pins are Hugging Face LFS metadata, checked October 7, 2026.
Each publisher card declares Apache-2.0. This is not a legal review.

Downloads resume into a per-model partial file (`curl.exe` on Windows; fallback
Invoke-WebRequest restarts). Both the exact size and SHA-256 must match before the
active model is replaced. On download/checksum failure the old model is untouched.
The chooser does not start Docker or connect the assistant. It records model.id and,
when .env exists, updates only AI_MODEL_NAME, AI_MODEL_SHA256 and an estimated
AI_MEM_LIMIT. Other settings/secrets are kept. The Windows start command recreates
only ai-runtime to apply a changed file/config; connect passes the selected model ID.
Linux: `docker compose --profile ai up -d --force-recreate ai-runtime`, then connect
using the selected ID if needed. No inference success is claimed by downloading.

Air-gapped bundles include the chooser/catalog, but network model download is
blocked there. Bring a verified GGUF via `--ai-model` / `-AiModel`. Listing always works offline.

## Sources

- https://huggingface.co/unsloth/Qwen3-1.7B-GGUF
- https://huggingface.co/unsloth/Qwen3-4B-GGUF
- https://huggingface.co/unsloth/Qwen3-8B-GGUF
- https://huggingface.co/unsloth/gpt-oss-20b-GGUF
- https://huggingface.co/unsloth/gpt-oss-120b-GGUF/tree/main/Q4_K_M
- API metadata: https://huggingface.co/api/models/unsloth/Qwen3-1.7B-GGUF/tree/main
  (same /api/models/ path pattern was checked for all five repositories).

## Tests and remaining work

Shell: fake-curl chooser checks cover list/no-network, recommendations, unknown IDs,
manual-only, RAM/disk gates, offline and airgap blocks, SHA/size failures, network
failure, replacement and config preservation, explicit override and skip.
PowerShell 7 on Linux: mocked chooser logic and actual hashes/files; all .ps1 parsed.
Installer and upgrade shell regressions also run. No real Windows, real Docker,
new-model inference or whole-stack RAM test ran. Remote CI is not green: Actions
minutes are exhausted, with no billing change or rerun attempt. Split 120B automatic
installation, Windows runtime tests and benchmarking larger models remain open.
