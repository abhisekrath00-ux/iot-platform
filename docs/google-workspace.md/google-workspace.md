# Google Workspace connection (design + current state)

Agreed scope (todo: Add Google Workspace connections to HexThings): Google OIDC sign-on,
Gmail send / email-assistant, Calendar on-call sync, Drive/Sheets report export.
Cleared to build: local implementation and simulator tests. NOT cleared (gated): real-account
OAuth, any live Google call, sending real mail. This file tracks what exists and what does not.

## Security model (fixed up front)

- Air-gapped default OFF: without GOOGLE_CLIENT_ID/GOOGLE_CLIENT_SECRET the connection does
  not exist (`ErrDisabled`), mirroring the OIDC SSO pattern. No background dial-outs.
- OAuth client is deployment-level (env), like OIDC_* today. User grants are per-tenant.
- Refresh tokens live in the tenant-scoped secrets store (`google_refresh_token`),
  AES-256-GCM at rest, write-only through the API - never in a table, log or error.
- Scopes are per-surface and minimal: gmail.send, calendar.readonly, drive.file. Consent uses
  access_type=offline + prompt=consent + include_granted_scopes so a tenant grants only the
  surfaces it enables, and re-consent always re-issues a refresh token.
- Access tokens are minted per call batch and never persisted.
- Token/permission errors surface the Google reason (invalid_grant etc.) and never the client
  secret or token material.
- Outbound message safety: recipient is validated (single clean address, no CRLF/whitespace),
  headers are CR/LF-stripped, non-ASCII subjects get RFC 2047 encoding. The Gmail API sends as
  the connected account.

## Built (server/internal/googlews, simulator-tested)

- Provider: consent URL builder, authorization-code exchange, refresh. Every Google endpoint
  injectable; tests use an httptest simulator - no live Google call exists in the test suite.
- Tenant token persistence over the secrets store: save (never overwrite with an empty refresh
  token), load, disconnect; tenant isolation checked.
- GmailSender: RFC 2822 plain-text send via /gmail/v1/users/me/messages/send with the tenant's
  grant; simulator asserts the exact request and the fail-closed path when unconnected.

Tests: 11 go tests in internal/googlews (consent params, exchange/refresh forms, error
surfacing without secret leakage, persistence round-trip + isolation + no-clobber, gmail send
shape, header injection refusal, bad recipient, unconnected fail-closed). gofmt/vet clean.

## NOT built yet (next increments)

- API routes: connect (admin consent redirect), callback, disconnect, connection status;
  tenant-RBAC wiring (admin only) and state store (reuse oidcstate pattern).
- Notification channel type "gmail" (alongside SMTP) using GmailSender.
- Calendar on-call sync worker (calendar.readonly) and Drive/Sheets report export (drive.file).
- SSO hosted-domain (hd) restriction on top of the generic OIDC provider.
- Web settings UI (connection card, consent entry, per-surface enable, honest status).
- Real-tenant OAuth and live-send verification - GATED, needs the user's Google account and
  explicit approval; the simulator tests are not evidence of live behavior.
