# Installing the edge agent (Ubuntu and Windows, x86_64 and arm64)

Packages are built by `scripts/build-edge.sh` (or the `release-edge` workflow on a
`v*` tag): `hexmon-edge-<ver>-<os>-<arch>.{tar.gz,zip}` plus `SHA256SUMS`.
The agent is one static Go binary: no runtime, no CGO, nothing to install. Verify
the download with `sha256sum -c SHA256SUMS`.

## Linux (Ubuntu, Debian, Raspberry Pi OS, any systemd distro)

```bash
tar xzf hexmon-edge-<ver>-linux-<amd64|arm64>.tar.gz && cd hexmon-edge-*
sudo ./install-linux.sh -claim-api https://api.example -claim-code CODE -claim-serial SERIAL
# or place a generated /etc/hexmon/edge-agent.yaml, then:
sudo systemctl start hexmon-edge && journalctl -u hexmon-edge -f
```

Installs to `/usr/local/bin/edge-agent`, creates an unprivileged `hexmon` user (in `dialout` for
serial ports), state in `/var/lib/hexmon-edge`, config `/etc/hexmon/edge-agent.yaml`, and a hardened
systemd unit (NoNewPrivileges, ProtectSystem=strict, private /tmp, auto-restart).

## Windows 10/11, Server 2019+ (x64 and arm64)

Elevated PowerShell:

```powershell
Expand-Archive hexmon-edge-<ver>-windows-<amd64|arm64>.zip .; cd hexmon-edge-*
.\install-windows.ps1 -ClaimApi https://api.example -ClaimCode CODE -ClaimSerial SERIAL
Start-Service HexmonEdge
```

Installs to `%ProgramData%\Hexmon\edge` (ACL-restricted to SYSTEM and Administrators, since identity keys live
there) and registers the `HexmonEdge` service with automatic start and restart-on-failure. Serial devices use
`COM3`-style names. If SmartScreen or AV flags an unsigned binary, code-signing the `.exe` is a release-process
step that is not done yet.

## Debian/Ubuntu package (.deb, amd64 and arm64)

`hexmon-edge_<ver>_<amd64|arm64>.deb` is the alternative to the tarball on apt-based systems:

```bash
sudo apt install ./hexmon-edge_<ver>_arm64.deb     # or: sudo dpkg -i ...
edge-agent -check-config                            # reads /etc/hexmon/edge-agent.yaml
sudo systemctl start hexmon-edge
```

It installs `/usr/bin/edge-agent`, the systemd unit and `/etc/hexmon/edge-agent.yaml` (a conffile, so
upgrades keep your edits). It enables the service but does not start it on first install; on upgrade it
restarts only a service that was already running. Pick ONE install method per machine (the tarball script
uses `/usr/local/bin`, the deb uses `/usr/bin`).

## Commands every install has

| Command | What it does |
| --- | --- |
| `edge-agent -version` | Version and OS/arch of this binary. |
| `edge-agent -check-config` | Loads the config and lints it: missing broker host, TLS files that do not exist, duplicate device or point ids, serial port names that do not fit the OS (`COM3` vs `/dev/ttyUSB0`), Modbus address range, a status page bound beyond localhost. Exit 1 on errors. The systemd unit runs it before every start, so a bad config fails loudly in `journalctl` instead of crash-looping silently. |
| `edge-agent -list-ports` | Serial ports the OS reports. On Linux it says whether the current user can open each one and lists stable `/dev/serial/by-id/` names (use those in config so a re-plug does not rename the port). |
| `edge-agent -health` | Asks the running agent's local status page. Exit 0 healthy, 1 not running, 2 running but the broker is unreachable (readings are being buffered). Usable from monitoring or a scheduled task. |

## Upgrading

Verify and install in one step (checks the SHA-256 against `SHA256SUMS` and refuses on mismatch):

```bash
sudo ./update-linux.sh hexmon-edge-<ver>-linux-<arch>.tar.gz           # SHA256SUMS next to it
```
```powershell
.\update-windows.ps1 -Package hexmon-edge-<ver>-windows-<arch>.zip
```

Both run the package's installer, which keeps the old binary as `edge-agent.prev` (`.prev.exe` on Windows),
leaves config and identity alone, restarts the service only if it was running, and waits up to 60 s for
`-health` to answer. If it does not, the old binary is restored automatically. A broker outage during the
check does not count as an upgrade failure. For the .deb, use `apt install ./new.deb`.

## Uninstalling

`sudo ./uninstall-linux.sh [--purge]` or `.\uninstall-windows.ps1 [-Purge]`. Without purge, config and the identity
are kept (the identity cannot be recreated without a new claim code).

## Logs

- Linux: the journal (`journalctl -u hexmon-edge -f`).
- Windows service: `%ProgramData%\Hexmon\edge\data\logs\edge-agent.log`, rotated at 5 MB, 5 files kept. Use `-log-file PATH`
  (or env `HEXMON_LOG_FILE`) to log to a file on any OS.

## Serial ports per OS

- Linux: `/dev/ttyUSB0`, `/dev/ttyACM0`, or better `/dev/serial/by-id/...`. The service user must be in `dialout`
  (the installers do this). On Raspberry Pi OS the on-board UART is `/dev/serial0` and may need to be freed
  from the login console in `raspi-config`.
- Windows: `COM3`; for COM10 and above use `\\.\COM12`. Check the number in Device Manager or `-list-ports`.
- `-check-config` warns when a port name does not fit the OS the config is checked on.

## Status: what is verified and what is not

Verified here (Linux x86_64 sandbox, no root, no systemd): all four targets (linux and windows, amd64 and
arm64) cross-compile, and `go vet` is clean for linux/amd64 and windows/amd64; unit tests for config lint, log rotation, `-check-config` exit codes and
`-health` exit codes pass; the Linux install, upgrade (config preserved, `.prev` kept) and checksum
refusal were run for real against temporary directories with the systemd step skipped; the amd64 binary runs;
the arm64 binary was confirmed to be a static aarch64 ELF; the .deb files build and list the right contents.

NOT verified: running under real systemd (unit, `ExecStartPre`, restart, rollback path); the .deb being installed by
apt/dpkg (maintainer scripts never ran); anything on Windows (the PowerShell scripts and the service wrapper were
never executed, there is no PowerShell here); anything running on real arm64 hardware; serial I/O on Windows
or with a real adapter. Do one pilot install per target before rollout. The Windows `.exe` is not code-signed, so
SmartScreen or antivirus may complain.

## Local status page

The agent serves a read-only page at `http://127.0.0.1:8088` (broker connection, buffered message count, per-device last read, values and errors). It has no control or config-write paths and sends a strict CSP. Change or disable it with `ui.listen` (`off` disables). Binding to a non-loopback address exposes status to that network; firewall it. On a headless gateway use an SSH tunnel: `ssh -L 8088:127.0.0.1:8088 user@gateway`.


## Auto-detect

The agent looks for devices by itself (serial, local network, BACnet), read-only, and lists proposals on the local
page, with `edge-agent -discoveries`, and on the dashboard Scan page. `edge-agent -detect-now` runs a pass now.
`HEXMON_DATA_DIR` overrides the data directory. See `docs/edge-autodetect.md`.
