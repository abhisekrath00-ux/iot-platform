# Edge application downloads and lifecycle

The HexThings dashboard's **Edge setup** and **Add device** pages offer local,
authenticated installer downloads. Select the edge PC's OS and CPU explicitly:
Intel/AMD64 or ARM64. ARM64 does not mean ESP32 firmware.

- **Windows:** a real `*-setup.exe` bundles the agent and opens a Windows Forms
  installer/manager. It requests Administrator access, installs the `HexmonEdge`
  service and adds **HexThings Edge** to the Start menu. Install/enroll, import
  generated gateway YAML, select optional boot startup, then Start. The manager
  also has Stop, Health, logs and a retain-config uninstall. This is not the
  service executable renamed as an installer.
- **Ubuntu:** an executable `*.run` bundles the agent and guided terminal setup.
  Make it executable, then run with sudo. It installs the systemd service and
  **HexThings Edge** terminal operator menu (`sudo hexthings-edge`). The menu
  imports generated YAML and offers Start/Stop, optional boot startup, Health,
  logs and retain-config uninstall. A `.deb` application package is also offered
  under Advanced packages. Pick one install method per PC; do not mix tar/run
  and deb installations. This version has no Ubuntu graphical setup wizard.

**Boot startup is optional.** A new installation is manual, not forced enabled.
The setup can save an affirmative startup preference, but it is activated only
when a non-example configuration passes the agent's config check. This checks
configuration, not physical sensor or broker connectivity. Start now is a
separate operator action. Running as an OS service means the edge does not need
a person to stay signed in.

## Delivery and integrity

`bash scripts/build-edge.sh 0.2.0` builds ten artifacts (two OS/CPU installer
pairs, archives and Debian packages), `SHA256SUMS` and `manifest.json`. The server
Docker image builds and contains these under `/edge-packages`; rebuilding the
server image is required to get the new dashboard downloads on an existing
installation. There is no automatic deployment to a user's server, no external
release fetch and no public release/tag required for this local build label.
The server Dockerfile now uses the repository root context:
`docker build -f server/Dockerfile .`. The provided Compose files use that context.

`GET /v1/edge-packages` and `GET /v1/edge-packages/{package}` are inside the normal
authenticated API. The dashboard uses the same-origin authenticated download
helper, never a token in a query string. Filenames are derived from the validated
inventory. Missing, linked, changed or hash-mismatched packages fail closed with
an explicit error; there is no SPA fallback or off-site replacement. Package
bytes are checked against their SHA256 before a download is sent. No enrollment
code, user config or private key is bundled in a generic installer.

## Configuration and safety

Install-only is available before a connection is ready. Commission the gateway
in Add device, use the verified server API address and one-time claim code, assign
the sensor, then import its generated `edge-agent.yaml`. Review broker host,
port/TLS, certificate trust and physical sensor settings. Do not start the
unchanged example file. Installing does not make a loopback-only server reachable
from another PC and does not open firewall rules, bind public ports, install
public TLS, or authorize exposing the lab plaintext broker.

The local status page remains `http://127.0.0.1:8088` on the edge PC. Check health,
then the first actual sensor value in the dashboard. "Installed", "running" and
"telemetry received" are different states. Broker-unreachable health is degraded,
not a successful connection. Existing config/identity is retained on upgrades;
an upgrade can restart a previously running service, so plan a maintenance
window and keep a backup. Uninstall retains identity/config/queue by default.
Explicit purge is destructive and is never part of normal setup or uninstall.

## Acceptance boundary

Cross-builds, package inventory/hash/self-extractor/PE checks, API integrity
refusal tests, web UI/download fixtures and startup/config contracts are useful
but are not a real Windows11/Ubuntu service install, UAC/SmartScreen acceptance,
boot/reboot test, hardware commissioning or live sensor/broker acceptance.
The Windows executable is unsigned. Windows and Ubuntu labels select intended
package targets; they do not claim all supported host versions passed lifecycle
acceptance. Native evidence must name the exact tested host and actions.

## Design sources checked 10 November 2026

- [Ignition native installers, service, commissioning, upgrade/backup and hashes](https://www.docs.inductiveautomation.com/docs/8.1/getting-started/installing-and-upgrading).
- [Kepware System Service vs Interactive runtime](https://www.ptc.com/help/kepware/kepware_server/en/kepware/server/runtime-settings-runtime-process.html).
- [ThingsBoard Ubuntu package and service](https://thingsboard.io/docs/iot-gateway/installation/deb-installation/) and [config-preserving upgrade checks](https://thingsboard.io/docs/iot-gateway/installation/upgrade-instructions/).
- [Siemens protected onboarding credentials](https://docs.industrial-operations-x.siemens.cloud/r/en-us/v26.09/industrial-edge-platform-operation-build-a-device/introduction/security-guidelines-for-devices/mandatory/handling-of-onboarding-credentials).
- [Microsoft automatic vs demand service startup](https://learn.microsoft.com/en-us/windows/win32/msi/serviceinstall-table) and [SCM boot behavior](https://learn.microsoft.com/en-us/windows/win32/services/automatically-starting-services).

These are design references, not endorsements or proof that HexThings has the
same capability, certification or reliability as those products. Some products
default to boot startup; HexThings follows the user's request for an explicit
option instead.

## Simple setup development checkpoint (not released, 12 November 2026)

The current development tree adds **Connect / get config** to the Windows
manager. It uses the saved gateway identity, asks for no replacement claim,
fetches to a new staging file, checks it, then uses the stopped-service checked
import/backup path. Server address and serial are saved for recovery, not claim
codes. New enrollment still needs the one-time claim. The form is scrollable on
short screens. Native Windows visual/lifecycle acceptance of this new path is
still pending. Do not update an existing installation from this checkpoint.

The server address must be HTTPS; HTTP is accepted only at loopback for local
executable tests. The server must explicitly configure a reachable TLS MQTT
address (`EDGE_MQTT_HOST`, `EDGE_MQTT_PORT`, `EDGE_MQTT_TLS=true`). The client checks
its saved certificate/key, serial and CA trust before import. Downloaded setup
cannot select local paths, expose the status page, enable discovery, commands,
outputs or remote restart. Signed source updates are a separate unchecked
opt-in and need an existing valid server signing key. The code creates no key.

A saved key-only lab identity is deliberately **not** treated as an mTLS identity.
The present source has no same-identity lab-to-mTLS migration endpoint. Repeating
enrollment would create/replace identity and is not the recovery route. An
identity-preserving migration and the server's HTTPS/TLS deployment remain a
release gate for the existing lab. Do not expose its anonymous plaintext broker,
create another claim, or silently substitute an insecure setup to bypass it.

Local evidence: PowerShell 7.7 parser and fake-agent manager contracts; checked
staging/import, running-service refusal, failed fetch leaves saved recovery data
unchanged, explicit source opt-in and no enrollment call. These ran on Linux,
not Windows PowerShell 5.1, Windows Forms, UAC or a real service. Existing native
CI evidence belongs to the older published installer, not these changes.
