# Installing

One command, the same steps online and air-gapped.

| Where | Command |
|---|---|
| Linux, macOS, WSL, source checkout | `./scripts/install.sh` |
| Linux, macOS, WSL, air-gapped bundle | `./install.sh` (inside the extracted bundle) |
| Windows (Docker Desktop, WSL2) | double-click `install.bat`, or `powershell -ExecutionPolicy Bypass -File install.ps1` |

Add `--yes` (`-Yes` on Windows) to accept every default and ask nothing.

## One command (needs Docker)

Linux / macOS: `curl -fsSL https://raw.githubusercontent.com/abhisekrath00-ux/iot-platform/main/scripts/get.sh | bash`

Windows (PowerShell): `irm https://raw.githubusercontent.com/abhisekrath00-ux/iot-platform/main/scripts/get.ps1 | iex`

Both download the source into `hexthings/` and run the guided installer with defaults. They need the repo to be public (a private repo needs `git clone` with a token, then `bash scripts/install.sh`). Tested: `get.sh` syntax and the installer it calls (with a fake Docker). Never run end to end, and `get.ps1` never run at all (no Docker or Windows machine here).

## What it does

1. **Checks the machine:** Docker running, Compose v2, about 4 GB RAM, 10 GB disk, ports (web 8080, 8000, 1883, 5432). A busy web port stops the install with the fix; other busy ports are warnings. Nothing is started if a check fails.
2. **Setup:** writes `.env` with generated secrets (database password, token signing secret, 32-byte secrets key; file mode 600). An existing `.env` is never changed. Asks for a workspace name and administrator email.
3. **Local AI (one-command install: on by default, `HEXTHINGS_NO_AI=1` / `-NoAi` to skip):** the installer can download the small Qwen3-1.7B model (1.1 GB, checksum verified, CPU only, runs in about 1.5 GB RAM) and auto-connect the assistant. It is a stock model, not trained on HexThings. Details in docs/ai-runtime.md. Manual option: if you give a `.gguf` file (`--ai-model FILE`, or `model.gguf` next to the installer), it is copied to `models/`, checksummed, and the `ai` profile starts. No model means no AI; the platform works the same.
4. **Installs:** from a bundle it verifies checksums and loads `images.tar.gz` (no internet). From a source checkout it builds.
5. **Waits** up to 3 minutes for the health check, **creates the workspace and first administrator** with a generated password (sent to the creating tool on stdin, saved once to `install-credentials.txt`, mode 600), and prints the address and sign-in.

Re-running is safe: same `.env`, same administrator, images updated in place. Everything is logged to `install.log`.

## Options

`--admin-email E`, `--workspace ID`, `--web-port N`, `--ai-model FILE`, `--no-ai`, `--yes`.

## After install

Change the generated password and delete `install-credentials.txt`. For production switch sign-in to your SSO and remove `LOCAL_LOGIN=1` from `.env` ([security](security.md)). Email and Slack alerts are optional settings in `.env` and Settings.

## What is tested, and what is not

- **Tested:** `scripts/test-install.sh` runs `install.sh` against fake `docker` and `curl` programs: Docker-down stops cleanly, generated secrets and file modes, no overwrite on re-run, no second administrator on re-run, the password never appears in a command line, bad names refused, the AI profile.
- **Not run:** the real stack. There is no Docker on the development machine, so `docker compose up`, the image builds (including the new `tenantctl` binary in the API image), the bundle script and the AI image have never been run end to end. **`install.ps1` and `install.bat` have never been run, not even with a shim.**
- Memory: a 4 GB host is the stated minimum; this was not measured.

## The `hexthings` command (Windows PowerShell)

After install, open a new terminal: `hexthings status | start | stop | restart | logs [svc] [-Follow] | version | update | patch FILE.zip | backup | restore FILE | ai | open | help`. `update` backs up first, then fetches the newest files from GitHub (needs internet), keeps `.env`, models and backups, and rebuilds. `patch` does the same from a local zip (air-gapped). Tested: PowerShell 7 on Linux against a fake `docker` (status, version, backup, restore, patch, logs). **Not run on real Windows PowerShell 5.1 or with real Docker.** On Linux/macOS use `scripts/backup.sh`, `scripts/restore.sh` and `docker compose`.
