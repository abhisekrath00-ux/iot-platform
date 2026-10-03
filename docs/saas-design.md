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
| Tenant provisioning | `go run ./cmd/tenantctl create --id --name --admin-email [--password-stdin]`, `list`. One transaction, first admin created. |
| User management | `GET/POST /v1/users`, `PUT /v1/users/{id}` (role, name, disabled), `PUT /v1/users/{id}/password`. Admin session only: API keys and the assistant are refused. All audited. |
| Guards | One active admin always remains (row-locked check, so two concurrent changes cannot remove the last one). You cannot change or disable your own account. Cross-tenant ids answer 404. |
| Disable and revoke | A disabled user's existing token stops working on the next request, and they cannot sign in. The database role is authoritative on every session request, so a demotion applies to tokens already issued. A password change or admin reset revokes every session issued before it (`tokens_valid_after`). API keys and the assistant carry their own role and lifecycle and are unaffected. |
| Local sign-in | `POST /auth/login`, **only when `LOCAL_LOGIN=1`** (default off, so IdP deployments keep passwords out of the platform). PBKDF2-HMAC-SHA256 600k iterations from the standard library, per-user salt, 12 to 128 character passwords. Per-IP rate limit, per-account lockout (5 failures, 15 minutes), generic error for unknown email, disabled and wrong password, dummy hash for unknown emails, login success and failure audited. |
| Password change | `POST /v1/me/password` (needs the current password; works for customer-scoped users too), admin reset clears lockout. |
| UI | Sign-in page (password and SSO link), Users page, sign out, automatic return to sign-in when a session is rejected. |
| SSO | Unchanged: matches the verified email to an existing user; never auto-creates users. A disabled user is rejected by the per-request check. |

## RBAC today
- `admin`: everything (users, customers, secrets, API keys, features, branding, retention).
- `operator`: write operations such as flows, rules, reports, device changes, requesting and approving control (four-eyes still applies).
- `installer`: read-only plus bulk device import.
- `viewer`: read-only.
- Customer-scoped users: read-only devices, values and alerts inside their customer subtree (customers-design.md).
Permissions are enforced per endpoint by role checks in the handlers; there is no permission matrix editor.

## Threats considered
Credential stuffing (rate limit, lockout, generic errors, timing), password storage (salted PBKDF2, never logged, never returned), privilege escalation (admin-only, key/AI refusal, last-admin and self-change guards), cross-tenant management (tenant-scoped queries, 404), stale access after offboarding (per-request disabled check).

## Not built (honest limits)
- Custom roles or per-permission RBAC, groups, per-customer roles.
- Self-service sign-up, email invitations, email verification, password reset by email (an admin sets passwords).
- MFA at sign-in (SSO providers own MFA; TOTP exists only for approving control commands).
- Session list and per-device sign-out (revocation is all-sessions-before-a-time only). Tokens are not refreshed: they expire after 12 hours.
- Per-tenant quotas, billing, usage metering, per-tenant data export or deletion, tenant suspension.
- One tenant per email address; a person in two workspaces needs two addresses.
- Account lockout is per account, so an attacker can lock a known account out (denial of service); the admin reset clears it.
