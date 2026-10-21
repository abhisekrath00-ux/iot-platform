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
- API routes (admin-only, tenant-scoped, refused to customer-scoped users by the scopedAllows
  middleware): GET /v1/integrations/google/status (configured + connected), POST
  /v1/integrations/google/connect (mints a single-use 10-minute state, returns the consent
  URL), GET /auth/google/callback (public but state-bound; exchanges the code, stores the
  refresh token, redirects to /settings?google=connected), DELETE /v1/integrations/google.
  OAuth client comes from GOOGLE_CLIENT_ID/GOOGLE_CLIENT_SECRET/GOOGLE_REDIRECT_URL env.

Tests: 11 go tests in internal/googlews (consent params, exchange/refresh forms, error
surfacing without secret leakage, persistence round-trip + isolation + no-clobber, gmail send
shape, header injection refusal, bad recipient, unconnected fail-closed) plus API tests in
cmd/api: unit (RBAC refusal, 503 unconfigured, 400 validation, bad-state 403, status defaults)
and a PG-backed integration test (full connect -> consent -> callback -> status -> disconnect
against the Google simulator, incl. state single-use replay refusal). gofmt/vet clean.

- Notification channel type "gmail": alert rules and escalation steps deliver through the
  tenant's Google grant. rules.SendOne now carries the tenant so any channel type can bind
  tenant-scoped credentials; channel creation validates the target address, refuses when the
  deployment has no Google OAuth client (409) or the tenant has not consented yet (409), and
  the channel-test button exercises exactly the tenant's grant.

## NOT built yet (next increments)

- Ingest-service runtime wiring: cmd/ingest evaluates alert rules with an env-only notifier
  today, so a "gmail" alert channel answers errUnsupported there until ingest constructs the
  Google-capable notifier (needs GOOGLE_* env on the ingest service + secrets store). The
  channel-test path and the dispatch contract are done; the alert-loop wiring is the gap.
- Calendar on-call sync worker (calendar.readonly) and Drive/Sheets report export (drive.file).
- SSO hosted-domain (hd) restriction on top of the generic OIDC provider.
- Web settings UI (connection card, consent entry, per-surface enable, honest status).
- Real-tenant OAuth and live-send verification - GATED, needs the user's Google account and
  explicit approval; the simulator tests are not evidence of live behavior.
