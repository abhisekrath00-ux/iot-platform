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

Both download the source into `hexthings/`, then **check the prerequisites and install only what is missing** (`scripts/prereq.sh`, `scripts/prereq.ps1`), then run the guided installer. Already-present pieces are skipped. If something is missing the plan is printed and you are asked once (`-Yes` / `HEXTHINGS_YES=1` accepts). Check only, changing nothing: `bash scripts/prereq.sh --check` or `powershell -File scripts\prereq.ps1 -Check`.

- Linux: Docker Engine + Compose via Docker's official script (get.docker.com), docker service start, and adding you to the `docker` group. Needs sudo and the internet. macOS: Docker Desktop via Homebrew if present, otherwise a manual link.
- Windows: WSL2 (`wsl --install --no-distribution`), Docker Desktop (winget, else Docker's installer), then starts its engine. One Windows admin prompt. If Windows needs a restart it stops and says so; run the same command again after the restart. CPU virtualization off in BIOS, an old Windows build, or too little disk fail with the fix. Docker Desktop has its own license terms (larger companies may need a paid subscription; check docker.com/pricing).
- Tested: the decision logic and Linux flow with fake docker/sudo/curl (`scripts/test-prereq.sh`, 11 checks) and the Windows decision logic under PowerShell 7 on Linux (`scripts/test-prereq.ps1`, 9 checks). NOT tested: Docker's real install script on a real distribution, anything on real Windows (WSL install, winget, UAC prompt, reboot flow, Docker Desktop first start/EULA), macOS, air-gapped sites (the prerequisite install needs the internet; use the bundle there). AI is offered before download; unattended shell setup skips it unless explicitly selected. They need the repo to be public (a private repo needs `git clone` with a token, then `bash scripts/install.sh`). Tested: `get.sh` syntax and the installer it calls (with a fake Docker). Never run end to end, and `get.ps1` never run at all (no Docker or Windows machine here).

## What it does

1. **Checks the machine:** Docker running, Compose v2, about 4 GB RAM, 10 GB disk, ports (web 8080, 8000, 1883, 5432). A busy web port stops the install with the fix; other busy ports are warnings. Nothing is started if a check fails.
2. **Setup:** writes `.env` with generated secrets (database password, token signing secret, 32-byte secrets key; file mode 600). Existing secrets are kept. Explicit model selection updates only AI runtime settings. Asks for a workspace name and administrator email.
3. **Local AI (optional, explicit model choice or skip):** the installer offers five model sizes (120B manual-only) or skip, and can download the small Qwen3-1.7B model (1.1 GB, checksum verified, CPU only, runs in about 1.5 GB RAM) and auto-connect the assistant. It is a stock model, not trained on HexThings. Details in docs/ai-runtime.md. Manual option: if you give a `.gguf` file (`--ai-model FILE`, or `model.gguf` next to the installer), it is copied to `models/`, checksummed, and the `ai` profile starts. No model means no AI; the platform works the same.
4. **Installs:** from a bundle it verifies checksums and loads `images.tar.gz` (no internet). From a source checkout it builds.
5. **Waits** up to 3 minutes for the health check, **creates the workspace and first administrator** with a generated password (sent to the creating tool on stdin, saved once to `install-credentials.txt`, mode 600), and prints the address and sign-in.

Re-running keeps existing secrets and administrator; images update in place. Explicit model choice can update AI runtime settings. Everything is logged to `install.log`.

## Upgrading and rolling back

`./scripts/upgrade.sh` updates an existing install without losing data:

1. **Preflight:** Docker, an existing `.env`, a clean checkout (uncommitted changes stop it before anything happens), and, for bundles, verified SHA256SUMS before anything is loaded.
2. **Backup first:** a fresh Postgres dump via `scripts/backup.sh`. If the backup fails, the upgrade stops - it never updates without a backup.
3. **Update:** source checkouts fast-forward with git (`git fetch` + merge `--ff-only` only); bundle installs load the new `images.tar.gz`. "Already up to date" is a no-op.
4. **Apply and verify:** `docker compose up -d` and a health check.
5. **Automatic rollback:** if the apply or the health check fails, the previous revision is checked out, the pre-upgrade backup is restored, services restart, and the health check runs again. The final message says plainly which outcome happened.

Each upgrade records its starting point in `.upgrade-state`; `./scripts/upgrade.sh --rollback` returns to that revision and database on demand. Everything is logged to `upgrade.log`.

Tested: `scripts/test-upgrade.sh` runs the whole flow (preflight refusals, backup-first order, fast-forward, no-op, automatic rollback on apply and health failures, explicit `--rollback`, bundle checksum verification and refusal) against fake docker/curl programs and a real local git remote - 15 checks, no Docker needed. **Never run against the real compose stack or a real Docker host**, so the rebuild and health behavior of a true upgrade is unproven. Signed (not just checksummed) bundles are not built yet; checksums catch damage, not a forged bundle.

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

Free the model's RAM without stopping the platform: `hexthings ai stop` (start again with `hexthings ai start`). `hexthings stop` stops everything including the model. Plain docker: `docker compose --profile ai stop ai-runtime`, `docker compose --profile ai up -d ai-runtime`, `docker compose --profile ai stop`. With the model stopped the platform is unaffected and the assistant panel reports that no model answers. Tested: PowerShell 7 parse and the commands against a fake docker (Linux); not on Windows or real Docker.

## The hexthings terminal menu, diagnostics and dashboard (Windows)

Running `hexthings` with no arguments opens an interactive menu (arrow keys, Enter, or the number keys 1-9; q quits): status, live dashboard, open the app, start/stop/restart, local AI, update, backup and restore, Diagnostics, Maintenance, Dev tools, version and edge help. Every command still works directly: `hexthings status`, `hexthings dash`, `hexthings diag`, `hexthings support`, `hexthings prune|vacuum|reindex`, `hexthings shell [service]`, `hexthings apitest`, `hexthings info`.

- `diag` checks docker, each service, ports (web, MQTT, Postgres, API), the database, free disk, clock against the web server, the local AI runtime and error lines in recent logs.
- `support` writes `hexthings-support-<time>.zip` with versions, service state, a redacted `.env` and the last 1000 log lines per service, with passwords, tokens, keys and database URLs redacted. Read it before you share it.
- `dash` is a live view: per-service CPU (bar and sparkline) and memory from `docker stats`, database size and alert counts from the database, web health. It shows only what those sources return.
- Plain fallback: with `NO_COLOR`, redirected output or no interactive console the banner is plain text and `hexthings` prints the help instead of the menu. A console that is not UTF-8 draws `#` blocks and `<3` instead of the heart. Set `HEXTHINGS_ASCII=1` to force that, `HEXTHINGS_NO_ANIM=1` to skip the reveal.

Tested: parsed with PowerShell 7 and run on Linux against a fake `docker` (menu navigation, diagnostics, support bundle redaction, dashboard rendering, plain and ASCII fallbacks). NOT run on Windows PowerShell 5.1 or against a real Docker. The Linux/macOS installer (`install.sh`) has the same wordmark; there is no `hexthings` menu for Linux/macOS yet.

### Choose a local AI size

The guided installer and Windows `hexthings` > Local AI menu now offer five pinned
open-license artifacts plus skip, with host resources, download size, conservative
fit estimates and tested/untested labels. See [model chooser](model-chooser.md).
`--ai-model-size ID` / `-AiModelSize ID` chooses explicitly; unattended setup no
longer downloads AI by default. Offline GGUF flags still work. 120B is manual-only.
