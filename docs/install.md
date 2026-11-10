# Installing

One command, the same steps online and air-gapped.

| Where | Command |
|---|---|
| Linux, macOS, WSL, source checkout | `bash scripts/install.sh` |
| Linux, macOS, WSL, air-gapped bundle | `./install.sh` (inside the extracted bundle) |
| Windows (Docker Desktop, WSL2) | double-click `install.bat`, or `powershell -ExecutionPolicy Bypass -File install.ps1` |

Add `--yes` (`-Yes` on Windows) to accept every default and ask nothing.

## Ubuntu Server: checked single-server bootstrap

`scripts/ubuntu-server.sh` is a standalone bootstrap intended for a public source download. Once the repository is public, download that file, inspect it, and run `sudo bash ubuntu-server.sh`. It asks before changes; `--yes` accepts the displayed plan and installer defaults, and `--check` performs local checks only. It requires a fresh destination (default `/opt/hexthings`), Ubuntu 22.04/24.04/26.04/26.10 amd64, at least 4000MiB RAM and 10GiB free disk. Those Ubuntu versions match Docker's current supported list, not a claim that this product has run on every version. arm64 is refused by this bootstrap pending actual installer verification.

It checks root rights, resources, existing destination, port conflicts, existing Docker/Compose and distribution-package conflicts before changes. Missing curl, Git, tar/gzip, iproute2, Python3 and CA certificates are installed with apt. For a host without Docker, it configures Docker's official Ubuntu apt repository and installs Engine, Buildx and Compose. It does not remove conflicting packages, replace an existing Docker lacking Compose, add users to the Docker group, change firewall rules or install paid services. Starting/enabling Docker changes the host; Docker has root-equivalent access. Failed later steps may leave dependencies or source installed, and a partially started stack must be inspected rather than assumed rolled back. Source builds require internet. AI is explicitly off.

All six default published ports now bind only to `127.0.0.1`: web8080, API8000, MQTT1883, PostgreSQL5432, Elasticsearch9200 and MCP8100. `WEB_PORT` now controls the web binding. This deliberately changes the prior all-interface defaults, including on reapply of an existing source installation. Remote gateways cannot use loopback MQTT; do not expose plaintext1883 as a workaround. Plan authenticated/TLS ingress separately. Docker-published ports may bypass ufw, so firewall-only advice is not a substitute for these bindings.

From your laptop, use `ssh -L 8080:127.0.0.1:8080 YOUR_SSH_USER@YOUR_SERVER`, then open `http://localhost:8080`. Complete the required first-sign-in email/password change. No public domain, certificate or TLS endpoint is configured by this bootstrap. Changing access requires a separate reviewed ingress setup.

Test boundaries: bootstrap tests cover supported/refused OS/architecture, resource boundaries, unsafe/mixed/host-network bindings, source binding contract, and stubbed main paths for successful source handoff, check-only, existing destination, busy ports and failed clone. Existing get/prerequisite/installer tests use stubs. CI additionally attempts the actual guided installer on a disposable Ubuntu runner, validates six live loopback host bindings, health/workspace creation, mode600 secrets and secret-preserving rerun. Read the exact run before calling that attempt passed. Fresh-host apt installation and every Ubuntu release remain unverified until exercised on such hosts; HA recovery fixtures do not prove this installer.

Docker installation source: https://docs.docker.com/engine/install/ubuntu/

## Private-source fallback: acquire source once, then run local setup

The repository is private as of 2026-10-31. Old anonymous `curl | bash` and
`irm | iex` raw-GitHub commands no longer work. Do not paste credentials into a
URL or command line, commit them, or make the repository public to bypass access.

Sign in to GitHub with an account the owner has granted repository access. Use
Code > Download ZIP, extract it, and open a terminal inside the extracted source
folder (the one containing `docker-compose.yml`). Alternatively use an existing
authenticated Git checkout. These scripts do not create accounts, mint tokens,
manage Git authentication or grant repository access. Downloading source is not
an integrity/provenance guarantee; use only the intended owner's source.

Linux/macOS/WSL:

```
bash scripts/get.sh --local-source .
```

Windows PowerShell (inside that source folder):

```
powershell -ExecutionPolicy Bypass -File scripts/get.ps1 -LocalSource .
```

The local-source mode validates required source files, does not download, pull,
merge or copy source, and then checks prerequisites before handing over to the
existing guided installer. It can operate on an extracted ZIP without `.git`.
The shell mode without `--local-source` can still clone/pull with Git's already
configured credentials; a failed pull now stops instead of installing stale files.
PowerShell without `-LocalSource` refuses with source-access instructions, before
any prerequisite/install changes. No new authentication flow is added.

Prerequisites and source image builds may still require internet. This is **not**
an offline deployment. Use the verified-image bundle path for air-gapped sites.
Docker/Compose must be usable. Prerequisite helpers install only missing pieces
after their normal confirmation, then invoke the installer. Prerequisite exit
codes (including restart/group-access stops) are preserved. PowerShell child
scripts use the same host executable, and the install exit code is preserved too.
Optional AI downloads still require the existing explicit choice.

Linux Docker prerequisites may need sudo/group membership; macOS may need Docker
Desktop. Windows may need WSL2, virtualization, UAC and a restart. Docker Desktop
has separate license terms; no license eligibility or paid plan is implied here.
Check without changing prerequisites: `bash scripts/prereq.sh --check` or
`powershell -File scripts/prereq.ps1 -Check`.

Tested: shell command-contract tests with stub prerequisites/installers/Git,
including no network command on the local path, incomplete-source refusal and
failed-pull refusal. PowerShell 7.6.9 on Linux executes local-source tests for paths
with spaces, missing-source refusal, prerequisite/install exit propagation and
AI argument forwarding. These are not native Windows, real private GitHub
download, Docker Desktop, prerequisite installation or full product install tests.

The shell setup adds the `hexthings` command to PATH when permitted. Run it for the
menu or use `bash scripts/setup.sh` directly. Windows menu scripts are installed
by the guided installer, not by downloading this bootstrap alone.

## What it does

1. **Checks the machine:** Docker running, Compose v2, about 4 GB RAM, 10 GB disk, ports (web 8080, 8000, 1883, 5432). A busy web port stops the install with the fix; other busy ports are warnings. Nothing is started if a check fails.
2. **Setup:** writes `.env` with generated secrets (database password, token signing secret, 32-byte secrets key; file mode 600). Existing secrets are kept. Explicit model selection updates only AI runtime settings. Asks for a workspace name and administrator email.
3. **Local AI (optional, explicit model choice or skip):** the installer offers five model sizes (120B manual-only) or skip, and can download the small Qwen3-1.7B model (1.1 GB, checksum verified, CPU only, runs in about 1.5 GB RAM) and auto-connect the assistant. It is a stock model, not trained on HexThings. Details in docs/ai-runtime.md. Manual option: if you give a `.gguf` file (`--ai-model FILE`, or `model.gguf` next to the installer), it is copied to `models/`, checksummed, and the `ai` profile starts. No model means no AI; the platform works the same.
4. **Installs:** from a bundle it verifies checksums and loads `images.tar.gz` (no internet). From a source checkout it builds.
5. **Waits** up to 3 minutes for the health check, **creates the workspace and first administrator** with the documented first-run login `admin@hexthings.com` / `Hex@2026` (or your own `--admin-email` with that password). It is stored only as a PBKDF2-SHA256 hash, flagged so the first sign-in shows a blocking pop-up to set your own email and password (the server refuses every other request until you do; the new password must be 12-128 characters, not the default, not your email). Nothing is written to a credentials file or the install log, and prints the address and sign-in.

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

### Windows source ZIP patch preflight

The private-source patch command validates a fresh local snapshot of the ZIP
before Docker checks, backup, source copying or restart. It requires one source
root and core source files, rejects traversal/absolute/Windows-special paths,
case-insensitive duplicates, links/special entries, file/directory collisions,
and top-level runtime paths such as `.env` (except `.env.example`), `.git`,
models, backups, certs and TLS keys. Archive size is capped at 256 MiB, expanded
size at 512 MiB, individual files at 128 MiB, and entries at 10,000. These are
source-patch limits, not offline-image-bundle support. Destination links and
file/directory conflicts are refused at preflight. A fresh unique staging folder
is removed on normal completion/failure. Hidden source files such as `.env.example`
are copied explicitly; archive-supplied runtime secrets are never accepted.

A valid archive still executes code supplied by its author: this is not a sandbox,
signature check or source-trust decision. Use only the intended owner's source.
The local snapshot digest identifies the bytes used, not their authenticity.
There is no lock against a concurrent local administrator changing destination
paths after preflight, no atomic all-files replacement, and no automatic rollback
for copy/build/health failures. The backup protects the database only, not source
or `.env`; keep an independent copy before maintenance. A process kill may leave
staging files. The existing `-NoBackup` override remains explicit and risky.

Tested with actual ZIP files and PowerShell 7 on Linux: safe extraction, malformed
paths, duplicates/case collisions, runtime paths, missing core source, link entries,
destination links/conflicts, unsafe input refusal before Docker/backup, and
actual source copying/hidden-file preservation/backup-before-copy with Docker,
backup and health stubbed. A simulated apply failure returns nonzero. Not
native Windows filesystem behavior, Docker restart or complete successful patch.

## Options

`--admin-email E`, `--workspace ID`, `--web-port N`, `--ai-model FILE`, `--no-ai`, `--yes`.

## After install

Complete the blocking first-sign-in email/password change. Current installers do not create `install-credentials.txt`; securely remove any old legacy copy if present. For production switch sign-in to your SSO and remove `LOCAL_LOGIN=1` from `.env` ([security](security.md)). Email and Slack alerts are optional settings in `.env` and Settings.

## What is tested, and what is not

- **Tested:** `scripts/test-install.sh` runs `install.sh` against fake `docker` and `curl` programs: Docker-down stops cleanly, generated secrets and file modes, no overwrite on re-run, no second administrator on re-run, the password never appears in a command line, bad names refused, the AI profile.
- **Installer-specific limit:** the local development machine has no Docker. Separate CI stack/image/HA checks exist, but do not by themselves prove guided installation. The new actual guided-installer CI step above must be read on the exact tip. Fresh-host apt provisioning, offline bundles, AI installation and native Windows remain separate unverified paths.
- Memory: a 4 GB host is the stated minimum; this was not measured.

## The `hexthings` command (Windows PowerShell)

After install, open a new terminal: `hexthings status | start | stop | restart | logs [svc] [-Follow] | version | update | patch FILE.zip | backup | restore FILE | ai | open | help`. `update` now refuses before backup or Docker changes because its old anonymous download cannot access this private repository. Sign in to GitHub, download the intended source ZIP, then use `hexthings patch FILE.zip`. The existing patch path backs up first, keeps `.env`, models and backups, and rebuilds; base images may need internet unless already cached. Patch has no signature/provenance validation or automatic rollback promise. Tested: PowerShell 7 on Linux against a fake `docker` (status, version, backup, restore, patch, logs). **Not run on real Windows PowerShell 5.1 or with real Docker.** On Linux/macOS use `scripts/backup.sh`, `scripts/restore.sh` and `docker compose`.

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
