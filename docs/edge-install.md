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

## Status

Cross-compiled for all four targets in CI on every push, unit-tested on Linux x86_64. The Windows service
wrapper and installer, and arm64 binaries, have not been run on real machines yet: do one pilot install per target
before rollout.
