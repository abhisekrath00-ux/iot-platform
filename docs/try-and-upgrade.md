# Try the latest build and update an older install

For a Windows (PowerShell) user with Docker Desktop. Linux/macOS/WSL steps are in [install.md](install.md).

## What is tested

- Tested (automated, with fake `docker`/`curl`, not a real Windows machine): `scripts/install.sh`, `scripts/upgrade.sh` (backup first, fast-forward, automatic rollback, `--rollback`), `scripts/backup.sh`/`restore.sh` mechanism, the terminal app menu through a pseudo-terminal.
- Tested for real: the API, web UI and report features (in headless Chrome on Linux with a real Postgres), and CI (go, web, compose smoke, security) on the repo.
- NOT tested: any of the PowerShell scripts (`get.ps1`, `install.ps1`, `hexthings.ps1`) on real Windows, Docker Desktop on Windows, WSL2, a real Docker stack of this exact latest build, Firefox/Safari/mobile. The PowerShell scripts were parsed and partly run under PowerShell 7 on Linux only. Expect to hit a rough edge; the steps below say what to do then.
- `setup.sh` (the animated terminal menu) is bash. It needs WSL2 (Ubuntu) or Git Bash with Docker Desktop running. It is not tested on Windows. On plain PowerShell use `hexthings.ps1` instead.

## 1. Check the latest build on a separate copy (do this first, leaves your old app alone)

1. Install Docker Desktop (WSL2 backend) and start it. About 4 GB free RAM and 10 GB disk.
2. In PowerShell, use a different folder and web port so nothing clashes with the old install:
   ```
   $env:HEXTHINGS_DIR = "$env:USERPROFILE\hexthings-new"
   irm https://raw.githubusercontent.com/abhisekrath00-ux/iot-platform/main/scripts/get.ps1 | iex
   ```
   If the old install is running, stop it first (`docker compose stop` in its folder) because the ports (8080, 8000, 1883, 5432) are the same. The installer stops with the fix if the web port is busy.
3. The installer generates secrets and the first admin, and saves the password once in `install-credentials.txt` in the new folder.
4. Open http://localhost:8080 and sign in as that admin.
5. To see the report features: left menu, Reports. Create a report, then try: the site / asset / device pickers (they narrow each other), grouping, highlight rules and expression conditions, and the download buttons PDF, Excel, XML, Word, PowerPoint.
6. Optional, WSL2 or Git Bash only: `bash scripts/setup.sh` opens the terminal menu (status, health, logs, backup, upgrade).

## 2. Update your existing install

If the old copy was installed with `get.ps1` (a downloaded zip, no `.git` folder), use the PowerShell command. It makes a backup first.

1. Open PowerShell in the old install folder (the one with `docker-compose.yml`).
2. Confirm Docker Desktop is running: `docker info`.
3. Back up yourself too, and copy the file somewhere else: `.\scripts\hexthings.ps1 backup` (writes into `backups\`). Also copy `.env` to a safe place; backups do not contain it.
4. Update: `.\scripts\hexthings.ps1 update`. It takes the backup, downloads the latest `main`, copies the files over (your `.env`, models, backups and credentials are not touched), rebuilds, restarts, and waits up to 3 minutes for a health check.
5. Open http://localhost:8080 and check Reports and your dashboards.

If the old copy is a git checkout (Linux/WSL): `./scripts/upgrade.sh` (or `bash scripts/setup.sh upgrade`). It refuses if you have uncommitted changes, backs up first, fast-forwards, restarts, health-checks.

Air-gapped: build a new bundle with `scripts/airgap-bundle.sh`, copy it over, run `upgrade.sh` (bundle path verifies checksums) or on Windows `hexthings patch FILE.zip`.

## Menu on Windows (single-node)

In PowerShell, in the install folder: `.\scripts\hexthings.ps1` opens a native PowerShell menu (arrows or numbers). Pick "Update" to run the same update as `hexthings.ps1 update`. If scripts are blocked: `powershell -ExecutionPolicy Bypass -File .\scripts\hexthings.ps1`. The installer does not put `hexthings` on PATH, so run it from the folder. The bash menu (`setup.sh`) is the one with the Upgrade item that calls `upgrade.sh`; it needs WSL2 or Git Bash. Untested on real Windows PowerShell 5.1.

## Database changes

The API applies new SQL migrations on start. They are additive and idempotent, so starting twice is safe. Older databases get new tables and columns; nothing is dropped. There is no automatic database downgrade: rollback means restoring the backup.

## Rollback

- git checkout / bash: `./scripts/upgrade.sh --rollback` returns to the revision and database recorded by the last upgrade. A failed upgrade rolls back by itself (tested with fakes).
- Windows `hexthings update`: there is NO automatic code rollback. If the new version fails health, the data is untouched. To go back: `.\scripts\hexthings.ps1 restore backups\<file>.dump.gz` restores the database (type RESTORE when asked), and re-extract your old zip over the folder if you kept it. Keep a copy of the old folder before updating. This path is untested on real Windows.

## If something fails

`.\scripts\hexthings.ps1 logs`, `.\scripts\hexthings.ps1 status`, and `hexthings-support-*.zip` (redacted) from the support command. Send those, not your `.env`.
