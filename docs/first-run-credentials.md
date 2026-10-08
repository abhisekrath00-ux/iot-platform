# First-run credentials and password storage

## Fresh install
`scripts/install.sh` / `install.ps1` create the workspace with `tenantctl create --default-credentials`: login `admin@hexthings.com` (or the `--admin-email` you gave) with password `Hex@2026`. This only happens when the workspace does not exist yet; re-runs and updates never touch an existing administrator.

The default password is public (it is in this document), so the server makes it useless on its own:
- The account is flagged `must_change_credentials`. While flagged, the API answers every request except `GET /v1/me` and `POST /v1/me/credentials` with 403 `credentials_change_required`. This is checked on the server for every request, so hiding the pop-up in the browser opens nothing.
- The web app shows a blocking pop-up after sign-in. `POST /v1/me/credentials` needs the current password, a valid new email that is not `admin@hexthings.com`, and a new password of 12-128 characters that is not the default, not the current one and not the email. It ends every earlier session, clears failed-login counters and returns a fresh token.
- API keys and the assistant cannot use that endpoint and cannot be created while the flag is set.
- Sign-in rate limiting and lockout still apply to the default account (5 wrong passwords lock it for 15 minutes).

Risk that remains: between install and the first sign-in anyone who can reach the web port can sign in with the default and set their own credentials first. Keep the port closed (or on a trusted network) until you have signed in once. Sign-in via SSO is unaffected.

## Storage
- Passwords: PBKDF2-HMAC-SHA256, 600000 iterations, random 16-byte salt (`pbkdf2-sha256$...`), standard library only. The plain text is never stored, logged or audited. Backups contain the hashes only.
- The installer no longer generates a password or writes `install-credentials.txt`; the log never receives the password. An old `install-credentials.txt` from an earlier install still holds a plain-text password: the installer warns, delete it after changing that password.
- `.env` holds service secrets (database password, token-signing key, secrets key) in plain text by necessity, mode 600 on Linux/macOS, and is not part of backups. Device broker passwords and reset/invite tokens are stored as hashes.

## Update
Updates re-run migrations (additive, idempotent). Existing users get `must_change_credentials = false`; nobody is forced, reset or re-seeded, and passwords, users and data are kept (backup first, restore with the backup scripts).

## Tested / not tested
Tested: `TestIntegrationFirstRunCredentials` on real Postgres (hash format, default login, server block of other routes, 7 rejected change attempts, successful change, old credentials dead, migration re-run leaves a changed admin untouched, lockout of the default account); installer test with fakes (no credentials file, no password in log or .env); real run in headless Chrome (pop-up, server error shown, change, normal app). Not tested: real Windows installer, a real Docker install of the new image, SSO users, upgrade of a real old install.
