# Multi-user workspace layer (SaaS-style): design, status and limits

Status: first slice built and tested against a real Postgres. Not remote-CI verified. Nothing here has run on a real multi-tenant deployment or been independently reviewed.

## Model
- **Tenant = workspace.** Every row carries `tenant_id`; this is the isolation boundary and already covers devices, data, flows, reports, secrets, audit log and keys. Each tenant has its own users, devices, data and audit trail.
- **User** belongs to exactly one tenant (one account per email address, case-insensitive). **Role** is one of admin, operator, installer, viewer. Roles are fixed, not custom.
- **Customers** (see customers-design.md) optionally confine a user to a device subtree inside the tenant.
- Provisioning a tenant is an **operator action** (`tenantctl`, direct database access, no network surface), not public self sign-up.

## Built
| Piece | Detail |
| --- | --- |
| Quotas and usage | Operator sets limits with `tenantctl quota set --tenant ID\|* [--devices N] [--users N] [--api-keys N] [--customers N]` (-1 = unlimited; `*` is the default for tenants without a row), `quota show`. Enforced twice: a clear 409 in each create handler, and an atomic database trigger (per-tenant advisory lock) that holds under concurrent inserts. Users count pending invites; revoked or expired API keys do not count. A tenant admin sees use against limits in Settings (`GET /v1/usage`, plus 24h and 30d data-point counts) but cannot change limits. No payment processor, invoicing or plans exist, by design. |
| Self sign-up (opt-in) | Off by default. `SELF_SIGNUP=1` plus `LOCAL_LOGIN=1` enables `POST /auth/signup` and a "Create a workspace" page: a visitor makes a tenant (id = slug of the name plus 6 random hex, never chosen by the caller) and becomes its first admin. Needs no internet and sends no email, so the address is **not verified**. Safeguards: strict per-IP rate limit, password policy, a cap on total tenants (`SELF_SIGNUP_MAX_TENANTS`, default 50, checked under a lock), global email uniqueness, an audit row. Set a default quota (`tenantctl quota set --tenant '*' ...`) before enabling so new tenants are bounded. No CAPTCHA, email verification, or trial/expiry logic. |
| Tenant provisioning | `go run ./cmd/tenantctl create --id --name --admin-email [--password-stdin]`, `list`. One transaction, first admin created. |
| User management | `GET/POST /v1/users`, `PUT /v1/users/{id}` (role, name, disabled), `PUT /v1/users/{id}/password`. Admin session only: API keys and the assistant are refused. All audited. |
| Guards | One active admin always remains (row-locked check, so two concurrent changes cannot remove the last one). You cannot change or disable your own account. Cross-tenant ids answer 404. |
| Disable and revoke | A disabled user's existing token stops working on the next request, and they cannot sign in. The database role is authoritative on every session request, so a demotion applies to tokens already issued. A password change or admin reset revokes every session issued before it (`tokens_valid_after`). API keys and the assistant carry their own role and lifecycle and are unaffected. |
| Local sign-in | `POST /auth/login`, **only when `LOCAL_LOGIN=1`** (default off, so IdP deployments keep passwords out of the platform). PBKDF2-HMAC-SHA256 600k iterations from the standard library, per-user salt, 12 to 128 character passwords. Per-IP rate limit, per-account lockout (5 failures, 15 minutes), generic error for unknown email, disabled and wrong password, dummy hash for unknown emails, login success and failure audited. |
| Password change | `POST /v1/me/password` (needs the current password; works for customer-scoped users too), admin reset clears lockout. |
| Invitations | `POST/GET /v1/users/invites`, `DELETE /v1/users/invites/{id}`, `POST /auth/accept-invite`. Admin-only, needs LOCAL_LOGIN. One-time link, 7 days, token 256-bit random, only its SHA-256 stored, shown once, never listed. One open invite per address (a new one replaces it), accept is single-use (row lock), generic error for bad, used, expired or revoked tokens, rate limited like sign-in. The link carries the token in the URL fragment. The platform sends no email: the admin passes the link on. |
| UI | Sign-in page (password and SSO link), Users page, sign out, automatic return to sign-in when a session is rejected. |
| SSO | Unchanged: matches the verified email to an existing user; never auto-creates users. A disabled user is rejected by the per-request check. |

## RBAC today
- `admin`: everything (users, customers, secrets, API keys, features, branding, retention).
- `operator`: write operations such as flows, rules, reports, device changes, requesting and approving control (four-eyes still applies).
- `installer`: read-only plus bulk device import.
- `viewer`: read-only.
- Customer-scoped users: read-only devices, values and alerts inside their customer subtree (customers-design.md).
Permissions are enforced per endpoint by role checks in the handlers; there is no permission matrix editor.

## Custom roles
A custom role is a built-in base role (operator, installer or viewer, never admin) minus denied capability groups: control, flows, reports, dashboards, alerts, devices, fleet, assets, assistant, audit. Each denial is `write:<group>` (all non-GET requests under the group's paths) or `read:<group>` (everything). It can only take access away: the handlers still check the base role, and the deny list is applied in the same per-request check that applies the live role. It also limits the AI assistant for that user, and the UI hides pages whose read is denied. API keys carry their own role and are not affected. Admin only; a role in use cannot be deleted; editing the base role moves members at once. Limits: groups are path prefixes, not per-object permissions; no custom role can grant more than its base; no per-customer roles. Tested with the real middleware, including the assistant path and cross-tenant refusal.

## Threats considered
Credential stuffing (rate limit, lockout, generic errors, timing), password storage (salted PBKDF2, never logged, never returned), privilege escalation (admin-only, key/AI refusal, last-admin and self-change guards), cross-tenant management (tenant-scoped queries, 404), stale access after offboarding (per-request disabled check).

## Not built (honest limits)
- Per-object permissions, user groups, per-customer roles, roles that grant more than a base role.
- Self-service sign-up, emailed invitations (links are copied by an admin), email verification, password reset by email (an admin sets passwords). An invitation may be limited to one customer; the scope is applied when it is accepted.
- MFA at sign-in (SSO providers own MFA; TOTP exists only for approving control commands).
- Session list and per-device sign-out. "Sign out everywhere" exists (`POST /v1/me/sessions/revoke`, tested); revocation is all-sessions-before-a-time only. Tokens are not refreshed: they expire after 12 hours.
- Per-tenant quotas, billing, usage metering, per-tenant data export or deletion, tenant suspension.
- One tenant per email address; a person in two workspaces needs two addresses.
- Account lockout is per account, so an attacker can lock a known account out (denial of service); the admin reset clears it.
