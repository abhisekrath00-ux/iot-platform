> Private repository: sign in to GitHub, download/extract the source ZIP or use
> an authorized Git checkout before running any commands below. Open a terminal
> in that source folder. Anonymous raw-download commands are no longer supported.
> See [private setup](install.md) for limits and tested-vs-unverified status.

# Try the latest build and update an older install

For a Windows (PowerShell) user with Docker Desktop. Linux/macOS/WSL steps are in [install.md](install.md).

## What is tested

- Tested (automated, with fake `docker`/`curl`, not a real Windows machine): `scripts/install.sh`, `scripts/upgrade.sh` (backup first, fast-forward, automatic rollback, `--rollback`), `scripts/backup.sh`/`restore.sh` mechanism, the terminal app menu through a pseudo-terminal.
- Historical API/UI/CI proofs do not establish this latest build. Private-repository CI is currently blocked before job execution by a GitHub billing/spending-limit gate. Current local tests and unverified deployment areas are reported separately.
- NOT tested: any of the PowerShell scripts (`get.ps1`, `install.ps1`, `hexthings.ps1`) on real Windows, Docker Desktop on Windows, WSL2, a real Docker stack of this exact latest build, Firefox/Safari/mobile. The PowerShell scripts were parsed and partly run under PowerShell 7 on Linux only. Expect to hit a rough edge; the steps below say what to do then.
- `setup.sh` (the animated terminal menu) is bash. It needs WSL2 (Ubuntu) or Git Bash with Docker Desktop running. It is not tested on Windows. On plain PowerShell use `hexthings.ps1` instead.

> Local-source setup checks Docker/WSL2 and installs only missing prerequisites after confirmation. Source acquisition is separate. Not tested on real Windows.

## 1. Check the latest build on a separate copy (do this first, leaves your old app alone)

1. Install Docker Desktop (WSL2 backend) and start it. About 4 GB free RAM and 10 GB disk.
2. Extract an owner-downloaded source ZIP into a separate folder such as `hexthings-new`, and open PowerShell there. The local-source command uses that folder, not `HEXTHINGS_DIR`:
   ```
   powershell -ExecutionPolicy Bypass -File scripts/get.ps1 -LocalSource .
   ```
   If the old install is running, stop it first (`docker compose stop` in its folder) because the ports (8080, 8000, 1883, 5432) are the same. The installer stops with the fix if the web port is busy.
3. The installer generates deployment secrets and creates the first administrator with the documented first-run login; first sign-in forces changing email/password. It does not create a credentials file.
4. Open http://localhost:8080 and sign in as that admin.
5. To see the report features: left menu, Reports. Create a report, then try: the site / asset / device pickers (they narrow each other), grouping, highlight rules and expression conditions, and the download buttons PDF, Excel, XML, Word, PowerPoint.
6. Optional, WSL2 or Git Bash only: `bash scripts/setup.sh` opens the terminal menu (status, health, logs, backup, upgrade).

## 2. Update your existing install

If the old copy was installed with `get.ps1` (a downloaded zip, no `.git` folder), use the PowerShell command. It makes a backup first.

1. Open PowerShell in the old install folder (the one with `docker-compose.yml`).
2. Confirm Docker Desktop is running: `docker info`.
3. Back up yourself too, and copy the file somewhere else: `.\scripts\hexthings.ps1 backup` (writes into `backups\`). Also copy `.env` to a safe place; backups do not contain it.
4. Sign in to GitHub and download the intended source ZIP. Update with `.\scripts\hexthings.ps1 patch C:\path\to\source.zip`. It takes a backup, copies source files, rebuilds, restarts and checks health. The old anonymous `update` command now refuses before Docker or backup work. Patch requires trusted source and is not a signed-update/automatic-rollback guarantee.
5. Open http://localhost:8080 and check Reports and your dashboards.

If the old copy is a git checkout (Linux/WSL): `./scripts/upgrade.sh` (or `bash scripts/setup.sh upgrade`). It refuses if you have uncommitted changes, backs up first, fast-forwards, restarts, health-checks.

Air-gapped: build a new bundle with `scripts/airgap-bundle.sh`, copy it over, run `upgrade.sh` (bundle path verifies checksums) or on Windows `hexthings patch FILE.zip`.

## Menu on Windows (single-node)

In PowerShell, in the install folder: `.\scripts\hexthings.ps1` opens a native PowerShell menu (arrows or numbers). The old "Update" item explains the private-source restriction. Use the explicit `patch` command with the owner-downloaded ZIP. If scripts are blocked: `powershell -ExecutionPolicy Bypass -File .\scripts\hexthings.ps1`. The installer does not put `hexthings` on PATH, so run it from the folder. The bash menu (`setup.sh`) is the one with the Upgrade item that calls `upgrade.sh`; it needs WSL2 or Git Bash. Untested on real Windows PowerShell 5.1.

## Database changes

The API applies new SQL migrations on start. They are additive and idempotent, so starting twice is safe. Older databases get new tables and columns; nothing is dropped. There is no automatic database downgrade: rollback means restoring the backup.

## Rollback

- git checkout / bash: `./scripts/upgrade.sh --rollback` returns to the revision and database recorded by the last upgrade. A failed upgrade rolls back by itself (tested with fakes).
- Windows `hexthings update`: there is NO automatic code rollback. If the new version fails health, the data is untouched. To go back: `.\scripts\hexthings.ps1 restore backups\<file>.dump.gz` restores the database (type RESTORE when asked), and re-extract your old zip over the folder if you kept it. Keep a copy of the old folder before updating. This path is untested on real Windows.

## If something fails

`.\scripts\hexthings.ps1 logs`, `.\scripts\hexthings.ps1 status`, and `hexthings-support-*.zip` (redacted) from the support command. Send those, not your `.env`.
