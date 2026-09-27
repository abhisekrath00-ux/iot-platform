# Security model

Basis: NIST SP 800-82r3 (OT), ISA/IEC 62443, OWASP ISVS, MCP security best
practices, TUF. These inform the backlog; nothing here is a certification claim.

## Trust boundaries

1. **Field -> edge.** Isolated serial adapters, surge/ESD protection per site
   survey. udev-pinned device identity, sandboxed drivers, firmware/wiring
   inventory. Never wire unlike signal levels (TTL/RS-232/RS-485) directly.
2. **Edge -> platform.** Outbound-only connections, unique per-gateway key
   (TPM/secure element where available), mTLS, cert rotation + revocation,
   tight broker ACLs. No universal fleet secret, no internet-exposed SSH.
3. **Human -> platform.** OIDC/SSO, phishing-resistant MFA for admins,
   tenant-scoped RBAC plus site/asset attributes, least privilege, session
   limits, append-only audit. Break-glass is time-limited and reviewed.
4. **Platform -> ops.** Secrets manager, encryption at rest, tested
   backup/restore, signed images, SBOM, patch windows, tenant-isolation tests,
   segmentation, incident runbook.

## Physical control gating (release gate)

Monitoring and control are both in scope, but any code path that actuates
physical equipment must have, before merge:

- documented actuator, permitted commands and fail-safe state,
- interlocks at the device (software is never the only safety layer),
- policy check + recorded approver (`approved_by`) and short command TTL,
- idempotency analysis — no blind retries,
- hazard analysis sign-off by someone other than the change author,
- audit trail from request to measured outcome.

The `commands` API enforces the envelope; reviewers must reject bypasses.

## MCP / AI

Server-side MCP gateway, allowlisted read tools first (list-sites,
get-device-health, query-time-series, explain-alert). User's delegated identity
and tenant authorization on every call. Capped query ranges, rate limits,
source timestamps, full audit. Model output never becomes a serial command.
Write tools only after separate safety + authorization review. Red-team prompt
injection, cross-tenant queries, token audience, SSRF, overbroad tool calls.

## Lifecycle

Enrollment by one-time claim bound to tenant + serial; bootstrap credential is
separate from the operational certificate; duplicate enrollment refused.
Signed config and update metadata, staged cohorts, automatic rollback on failed
boot. Revoke and wipe keys on decommission. Document software support lifetime.
