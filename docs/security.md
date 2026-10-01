# Security model

Basis: NIST SP 800-82r3 (OT), ISA/IEC 62443, OWASP ISVS, MCP security best
practices, TUF. These inform the backlog; nothing here is a certification claim.

## Enforced in code today

- Rate limiting on unauthenticated paths (enrollment claim 5/min, SSO 20/min, per client IP, X-Forwarded-For not trusted).
- Security headers on all API responses (nosniff, frame DENY, no-referrer, CSP `default-src 'none'`, no-store).
- Enrollment claim codes: 160-bit, SHA-256 at rest, single-use, expiring, constant-time compare, identical error for unknown code vs serial mismatch (no oracle).
- OIDC: discovery issuer check, RS256 JWKS signature verification, single-use state + nonce (10 min TTL), SSO never auto-creates users.
- mTLS: CSR signature checked, cert CN bound to the claimed serial, client-auth EKU only, fingerprint stored for revocation.
- RBAC guards on every mutation; audit log for creates, claims, SSO logins, approvals.
- Input validation: allowlisted point ids / types / word orders / ops; parameterized SQL everywhere; escaped report HTML with no external assets.
- CI: gofmt gate, govulncheck on both Go modules, npm audit (high) on web.

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

## Threat model (STRIDE)

Scope: the edge agent on gateway SBCs, the broker, ingest, API, web, MCP
gateway, and the enrollment/SSO flows. Out of scope: physical site security
and the customer's own network (both covered by site-survey requirements).

Assets: telemetry (integrity, availability), tenant identity, device control,
credentials (certs, claim codes, refresh tokens), audit history, customer PII
in user accounts.

Actors: internet opportunist, malicious or compromised gateway, rogue
low-privilege tenant user, supply-chain attacker, insider with ops access.

| STRIDE | Concrete threat | Mitigation | Status |
|---|---|---|---|
| Spoofing | Rogue device publishes as a real gateway | mTLS with per-gateway certs, CN bound to claimed serial at enrollment, broker `use_identity_as_username`, fingerprint revocation | Enforced |
| Spoofing | Stolen claim code reused to enroll a second device | Single-use expiring codes, SHA-256 at rest, duplicate enrollment refused, bootstrap credential separate from operational cert | Enforced |
| Spoofing | Session/token forgery on SSO | RS256 JWKS verification, issuer check, single-use state + nonce | Enforced |
| Tampering | Payload lies about its tenant to corrupt another tenant's data | Ingest derives tenant from authenticated identity, never payload fields | Enforced |
| Tampering | Command replayed or modified in flight | mTLS channel, short command TTL, `request_id` dedupe, policy version pinned | Enforced |
| Tampering | Malicious config/profile injects SQL or driver ops | Allowlisted point ids/types/ops, parameterized SQL | Enforced |
| Tampering | Update or config swapped in supply chain | Signed config/update metadata, staged cohorts, automatic rollback | Backlog (fleet rollout) |
| Repudiation | Operator denies issuing a control command | Append-only audit from request to measured outcome, `approved_by` recorded | Enforced |
| Repudiation | Enrollment or SSO event disputed | Audit log for creates, claims, SSO logins, approvals | Enforced |
| Info disclosure | Cross-tenant read via API or MCP | Tenant scoping on every query, MCP carries delegated user identity + tenant authorization, red-team tests for cross-tenant queries | Enforced + recurring test |
| Info disclosure | Secrets leak via reports, logs, or bundle | Escaped report HTML with no external assets, no secrets in logs, bundle ships an env template only | Enforced |
| Info disclosure | Token or cert theft from a stolen gateway | Per-gateway keys (TPM where available), revocation, wipe on decommission | Partially (TPM is hardware-dependent) |
| DoS | Credential-stuffing / claim guessing | Per-IP rate limits on unauthenticated paths, no enumeration oracle on claim errors | Enforced |
| DoS | Gateway floods broker or ingest | Per-gateway ACLs and broker quotas, QoS 1 with app-level dedupe, backpressure to the offline queue | Partially (broker quotas per site survey) |
| DoS | Expensive report/query exhausts the API | Capped query ranges, rate limits, scheduled reports run through the same guards | Enforced |
| Elevation | Viewer role gains mutation rights | RBAC guards on every mutation path, four-eyes approval on control | Enforced |
| Elevation | Compromised edge driver escapes to the host | Sandboxed drivers, least-privilege agent user, no inbound SSH | Enforced (deployment) |
| Elevation | MCP write path actuates equipment | No write tools shipped; writes need separate safety + authorization review | Enforced by absence |

Recurring duties: dependency scanning in CI (govulncheck, npm audit),
quarterly threat-model review against shipped features, tenant-isolation
tests before GA, independent penetration test before enterprise GA.

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

## API keys

Keys can request commands but can never approve them: approval requires an interactive
user session (human four-eyes), enforced in `approveCommand` and covered by a test.

Each key is rate limited (token bucket, default 600 requests/min with a small burst, `API_KEY_RPM` to change, 0 disables). The bucket is in memory per API replica, so the cluster-wide ceiling is the rate times the replica count; it bounds a leaked key but does not replace a WAF. A key can also be limited to endpoint groups (the first path segment: devices, telemetry, alerts, dashboards, reports, flows, rules, points, profiles, search, export); anything else returns 403. Administrative groups (api-keys, audit, broker, commands, commissioning, enrollment, fleet, gateways, notifications) cannot be granted to a scoped key. An empty scope list means every endpoint the role allows, so keys created before scopes keep working. Scopes are coarse (endpoint group, not per-device or per-method); the role still decides read versus write.

Machine clients (SCADA, BI, scripts) use `Authorization: Bearer hxk_<id>.<secret>`.
- Only `sha256(secret)` is stored; the token is shown once at creation (`Cache-Control: no-store`) and compared in constant time.
- Keys are tenant scoped, limited to `viewer` or `operator` (never `admin`), must expire (max 365 days), and can be revoked immediately.
- Keys cannot list, create or revoke keys; those calls need an admin session.
- Create and revoke are audited; `last_used_at` is recorded. Key principals appear as `apikey:<id>` in audit and approval records.
- Not yet built: per-key endpoint scopes and per-key rate limits (the global limiter applies).
