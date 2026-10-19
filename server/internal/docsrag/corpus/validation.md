# Install validation route

How a deployment gets validated per operating system, and what each step proves.
Nothing here claims a check ran on an OS it did not run on; the executed vs
open status is tracked per gate at the bottom.

## 1. Host preflight (read-only, every OS)

- Linux: `scripts/validate-install.sh` - checks Docker presence, daemon
  reachability, compose plugin, memory, disk, required tools and default
  ports. It never installs Docker, never starts or stops services, and
  never touches ports, data or service accounts. With `--bundle DIR` it
  also verifies the bundle's SHA256SUMS. With `--rehearse DIR` (opt-in,
  asks first unless `--yes`) it boots the bundle's stack, waits for
  /healthz, and tears everything down again; that is the only mode that
  changes anything.
- Windows: `scripts/validate-install.ps1` - the same preflight natively
  (Docker Desktop, memory, disk, ports, optional bundle checksums). No
  rehearsal mode; the rehearsal is the Linux/WSL path. The .sh does not
  run natively on Windows; use WSL if the bash rehearsal is wanted there.

## 2. Endpoint readiness and auth (executed in CI-style runs)

`scripts/test-endpoint-readiness.sh` boots the real API binary against a
fresh Postgres database - migrations at startup, exactly like an install -
seeds the first-run admin through `tenantctl --default-credentials` (the
production seeding path), then measures and asserts:

- time-to-ready until /healthz answers 200, and warm /healthz latency
- /v1 refuses requests without a token (401), bad passwords are 401
- the documented first-run login works and is flagged
  `must_change_credentials`; while flagged every route except /v1/me and
  /v1/me/credentials is 403 `credentials_change_required`
- changing the credentials returns a fresh token, revokes the old one
  (tokens_valid_after), and retires the default login

## 3. Gate status (honest: executed vs open)

| Gate | Status |
|---|---|
| Endpoint readiness + first-run auth gate | EXECUTED locally: 10/10 checks pass against a real API on a fresh Postgres (349 ms to ready). Not a dry run. |
| Linux preflight script logic | Tested with fake docker (9/9): green host, missing docker, dead daemon, bundle checksums good/tampered, rehearsal refuses without a daemon, preflight proven read-only. |
| Windows preflight (.ps1) | WRITTEN, never executed (no PowerShell in the build environment). First real run is its test. |
| Isolated rehearsal (--rehearse) | WRITTEN, never executed (no Docker in the build environment). |
| Real Docker/compose install on Ubuntu | OPEN - needs a host with Docker. |
| systemd service operation | OPEN - needs a Linux host. |
| Native Windows install + service | OPEN - needs a Windows machine. |
| Real edge hardware/broker | OPEN - see docs/pilot-readiness.md. |
