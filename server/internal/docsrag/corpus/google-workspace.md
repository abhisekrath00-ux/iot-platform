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
- Every consumer enforces the recorded grant before touching Google (RequireScope): a partial
  grant (the surface's scope missing) or a legacy grant (no recorded scope list) fails closed
  with a clear reconnect instruction instead of attempting the call. Gmail send, calendar
  on-call sync and Drive upload each guard their own scope.
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

- Ingest-service runtime wiring: cmd/ingest wraps its notifier with the Gmail capability
  when GOOGLE_CLIENT_ID is set (cmd/ingest/googlews.go). The wrapper resolves the tenant
  grant through the same secrets store, so alert dispatch from the ingest loop sends through
  the tenant's connected Google account; Email/Slack delegate unchanged. A missing or
  invalid SECRETS_KEY on the ingest service is logged at startup and gmail sends fail closed
  with a clear error - telemetry ingest itself is never blocked by Google configuration.
  Sparkplug ingestion uses the same wrapped notifier.

- Calendar on-call sync (calendar.readonly): an escalation rotation links to a Google
  calendar; a 2-minute worker resolves the event spanning now and overrides the on-call
  channel until that event ends. Fail-closed in both directions: no current event, a summary
  that names no channel target in this workspace, a failed sync, or a sync older than 15
  minutes all fall back to the static rotation - external calendar data sharpens escalation
  but can never break it. PUT /v1/oncall/{id}/calendar links (validates readability with the
  tenant grant before persisting) and unlinks; listOnCall surfaces the override, source
  event, sync time and sync error; the Escalation UI links/unlinks and shows every state.
- Drive/Sheets report export (drive.file): POST /v1/reports/{id}/export/drive renders the
  report fresh and uploads a NEW file to the connected Drive - format "html" stores the
  rendered document, format "sheet" uploads the CSV render converted into a Google Sheet.
  GET /v1/reports/{id}/exports lists the per-report export history (report_drive_exports
  table, migration 0084). The platform uses the Drive permission only to upload new report
  files - nothing in HexThings reads, lists or changes existing tenant Drive content. Unconfigured deployment answers 503,
  unconnected tenant 409; the Reports page shows both buttons, the last successful export
  link and refused exports as alerts.

- Settings surface truth: the consent exchange records Google's returned scope string
  (google_granted_scopes in the secrets store; a narrower re-consent overwrites, never
  accumulates; disconnect forgets it) and GET /v1/integrations/google/status reports each
  surface granted / not granted from it ("unknown" for connections predating the record).
  The Settings card requests exactly the surfaces with live consumers and lists each with
  its true state.

- SSO hosted-domain restriction: OIDC_HOSTED_DOMAIN on the API restricts OIDC sign-in to
  accounts of that Google Workspace domain - the verified id_token's hd claim must match, a
  missing or different claim refuses the login. Unset keeps sign-in open to any account the
  provider issues (the default, unchanged). The check runs after signature, issuer, audience,
  expiry and nonce verification, inside the generic OIDC provider (any issuer that emits hd).

## NOT built yet (next increments)

- Nothing left on the agreed Workspace list; real-tenant OAuth and live-surface verification
  remain gated below.
- Real-tenant OAuth and live-send verification - GATED, needs the user's Google account and
  explicit approval; the simulator tests are not evidence of live behavior.
