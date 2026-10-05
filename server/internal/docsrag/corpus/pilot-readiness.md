# Pilot readiness

What to do before putting this platform in front of a real site, and what is still unproven. Written from the
code and tests in this repo on 2026-10-04. It does not claim the platform is production certified: no outside
security review, no load test on target hardware and no real-device pilot has happened.

## 1. Check the configuration (5 minutes)

Run the config audit with the same environment as the API:

    docker compose run --rm api /bin/api -check-config      # or, from a build: ./api -check-config

It prints `FAIL` and `WARN` lines and exits non-zero on any FAIL. Set `STRICT_CONFIG=1` to make the API refuse to
start while a FAIL remains. It never prints a secret. Checks: signing secret length and placeholders (the
`.env.example` value is refused), `SECRETS_KEY` format, plain-http public URL, unencrypted remote database,
unencrypted broker, open self sign-up, test switches left on.

## 2. Go / no-go checklist

| Item | How to check | Needed for pilot |
|---|---|---|
| Secrets generated, not copied from examples | `-check-config` has no FAIL | Yes |
| TLS in front of web and API; MQTT on 8883 with mTLS | deployment.md section 1 | Yes (WARN is acceptable only on a closed lab network) |
| Backups taken and one restore rehearsed | `scripts/backup.sh`, `scripts/restore.sh`, backup-restore.md | Yes |
| `SECRETS_KEY` backed up separately from the database | backup-restore.md | Yes if you use stored secrets |
| Behind the bundled nginx: `TRUSTED_PROXIES` set to the web container network (`docker network inspect`), API port 8000 not published | Compose file, security.md | Yes, else all users share one sign-in rate-limit bucket |
| First admin has MFA (TOTP) enabled | Users page | Yes |
| Tenant quotas set (`tenantctl quota`) | `GET /v1/usage` | Yes if more than one tenant |
| Self sign-up off (default) | `-check-config` | Yes unless you intend it |
| Air-gapped: nothing calls out | airgap.md; optional channels unset (SMTP, Teams, SMS, index sink) | Yes for on-prem |
| Alert delivery tested to a real recipient | Settings, notification channels, "Send test" (goes through the real alert delivery path) | Yes |
| Control actions: approval and four-eyes rehearsed with a simulated actuator | hazard-analysis.md | Yes before any real actuator |
| Edge agent installed on the real board and run for days | edge-install.md | Yes, never done here |
| Load figure measured on your hardware | slo.md, scaling.md | Yes, never done here |

## 3. Known gaps a pilot must accept

- Remote CI has not run since the Actions minutes ran out (2026-10-02). Everything was verified with the full local
  gate and an empty-database run before each push, but no remote-green claim exists.
- No external security review. The function node (sandboxed JavaScript) is off by default and unreviewed.
- Protocols and edge hardware are simulator-only. See status.md for the per-protocol label.
- Protocol writes are not built. Control goes through approval, four-eyes and the edge gate with a simulated
  actuator, and needs a hazard review before any real actuator is connected.
- Self sign-up (if enabled) does not verify email addresses.
- Optional email, Teams, SMS and index-sink integrations are tested against local fakes only.
- SAML needs an OIDC bridge (sso-saml.md). There is no native mobile app; use the PWA.

## 4. Suggested pilot shape

One tenant, one site, one gateway, five to twenty devices, read-only telemetry first. Run for two weeks. Turn on
alerts, then flows, then (only after a hazard review) control. Record real throughput and any false alerts, and
feed them back into status.md.
