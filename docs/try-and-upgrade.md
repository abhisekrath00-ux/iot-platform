> Public repository as of2026-11-10. Use the platform-specific server bootstraps
> in [installation](install.md), or an explicit local-source checkout/ZIP.
> The lightweight edge agent has [separate installation steps](edge-install.md).

# Try the latest build and update an older install

For a Windows (PowerShell) user with Docker Desktop. Linux/macOS/WSL steps are in [install.md](install.md).

## What is tested

- Tested (automated, with fake `docker`/`curl`, not a real Windows machine): `scripts/install.sh`, `scripts/upgrade.sh` (backup first, fast-forward, automatic rollback, `--rollback`), `scripts/backup.sh`/`restore.sh` mechanism, the terminal app menu through a pseudo-terminal.
- Actual Ubuntu Desktop24.04.5 amd64 server bootstrap passed apt Docker installation, seven services, health, workspace/admin, six loopback bindings and automatic animated-menu handoff. [Desktop run5](https://github.com/abhisekrath00-ux/iot-platform/actions/runs/42785598056).
- Native Windows Server2025 PowerShell5.1+7 parsing/pure server-bootstrap contracts passed [run1](https://github.com/abhisekrath00-ux/iot-platform/actions/runs/42792052432). Full Windows11/WSL2/Docker Desktop installation and actual Windows menu/edge service remain unverified.
- General [CI1549](https://github.com/abhisekrath00-ux/iot-platform/actions/runs/42792052397) passed all four jobs at d276fb6. The earlier private-repository billing gate is historical, not a current blocker. Physical devices, all Ubuntu versions, AI setup and production HA are not established by these results.
- `setup.sh` (the animated terminal menu) is bash. It needs WSL2 (Ubuntu) or Git Bash with Docker Desktop running. It is not tested on Windows. On plain PowerShell use `hexthings.ps1` instead.

> Local-source setup checks Docker/WSL2 and installs only missing prerequisites after confirmation. Source acquisition is separate. Not tested on real Windows.

## 1. Check the latest build on a separate copy (do this first, leaves your old app alone)

1. Install Docker Desktop (WSL2 backend) and start it. For the current Windows server bootstrap: Windows11 client23H2+, Intel/AMD x64,8GiB RAM,20GiB free disk and virtualization. Docker license eligibility remains your responsibility.
2. Extract the intended repository source ZIP into a separate folder such as `hexthings-new`, and open PowerShell there. The local-source command uses that folder, not `HEXTHINGS_DIR`:
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
4. Download the intended source ZIP from the public repository (sign in only where access requires it). Update with `.\scripts\hexthings.ps1 patch C:\path\to\source.zip`. It takes a backup, copies source files, rebuilds, restarts and checks health. The `update` command remains deliberately disabled before Docker or backup work; public visibility does not restore that code path. Patch requires trusted source and is not a signed-update/automatic-rollback guarantee.
5. Open http://localhost:8080 and check Reports and your dashboards.

If the old copy is a git checkout (Linux/WSL): `./scripts/upgrade.sh` (or `bash scripts/setup.sh upgrade`). It refuses if you have uncommitted changes, backs up first, fast-forwards, restarts, health-checks.

Air-gapped: build a new bundle with `scripts/airgap-bundle.sh`, copy it over, run `upgrade.sh` (bundle path verifies checksums) or on Windows `hexthings patch FILE.zip`.

## Menu on Windows (single-node)

In PowerShell, in the install folder: `.\scripts\hexthings.ps1` opens a native PowerShell menu (arrows or numbers). The "Update" item directs you to an explicit source-ZIP patch; automatic source download remains disabled. Use the explicit `patch` command with the owner-downloaded ZIP. If scripts are blocked: `powershell -ExecutionPolicy Bypass -File .\scripts\hexthings.ps1`. The guided Windows installer attempts to add its command wrapper to your user PATH. Open a new terminal, or use the explicit script path from the source folder. The bash menu (`setup.sh`) is the one with the Upgrade item that calls `upgrade.sh`; it needs WSL2 or Git Bash. Untested on real Windows PowerShell 5.1.

## Database changes

The API applies new SQL migrations on start. They are additive and idempotent, so starting twice is safe. Older databases get new tables and columns; nothing is dropped. There is no automatic database downgrade: rollback means restoring the backup.

## Rollback

- git checkout / bash: `./scripts/upgrade.sh --rollback` returns to the revision and database recorded by the last upgrade. A failed upgrade rolls back by itself (tested with fakes).
- Windows source-ZIP `hexthings patch`: there is NO automatic code rollback. If the new version fails health, the data is untouched. To go back: `.\scripts\hexthings.ps1 restore backups\<file>.dump.gz` restores the database (type RESTORE when asked), and re-extract your old zip over the folder if you kept it. Keep a copy of the old folder before updating. This path is untested on real Windows.

## If something fails

`.\scripts\hexthings.ps1 logs`, `.\scripts\hexthings.ps1 status`, and `hexthings-support-*.zip` (redacted) from the support command. Send those, not your `.env`.
