# First-run credentials and password storage

## Fresh install
`scripts/install.sh` / `install.ps1` ask for the administrator email and a password you choose, then ask you to confirm the hidden password. They create a new administrator through `tenantctl create --password-stdin`. The password must be 12-128 UTF-8 bytes and different from the email. It is never printed or saved to `.env`, the log or a credentials file. New accounts do not use a default password or require a first-sign-in replacement of the chosen password.

For a new unattended install, explicitly use Bash `--admin-password-stdin` or PowerShell `-AdminPasswordStdin` and supply one private line through stdin. `--yes` / `-Yes` alone refuses new-account creation. Do not place the password itself in shell history, command arguments or environment variables. Existing workspace reruns never change the administrator or password. Failed workspace-list reads stop setup; an email already used by another workspace is an error, not permission to reset that account.

## Legacy first-run accounts
Older installers used `tenantctl create --default-credentials`: `admin@hexthings.com` (or the supplied email), password `Hex@2026`. The explicit CLI compatibility mode remains available, but new guided installations do not use it.

The default password is public (it is in this document), so the server makes it useless on its own:
- The account is flagged `must_change_credentials`. While flagged, the API answers every request except `GET /v1/me` and `POST /v1/me/credentials` with 403 `credentials_change_required`. This is checked on the server for every request, so hiding the pop-up in the browser opens nothing.
- The web app shows a blocking pop-up after sign-in. `POST /v1/me/credentials` needs the current password, a valid new email that is not `admin@hexthings.com`, and a new password of 12-128 UTF-8 bytes that is not the default, not the current one and not the email. It ends every earlier session, clears failed-login counters and returns a fresh token.
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
Chosen-password installer tests cover actual Bash PTY hidden entry, confirmation, weak-password refusal, stdin provisioning, no password in argv/log/`.env`, existing-workspace preservation and failed-list refusal with Docker/network stubs. PowerShell secure-input/UTF8 stdin contracts are stub-tested. Native Windows and actual Docker new-account/login/rerun acceptance run separately in CI; a user-host install is not implied by these tests.

Legacy behavior tests: Tested: `TestIntegrationFirstRunCredentials` on real Postgres (hash format, default login, server block of other routes, 7 rejected change attempts, successful change, old credentials dead, migration re-run leaves a changed admin untouched, lockout of the default account); installer test with fakes (no credentials file, no password in log or .env); real run in headless Chrome (pop-up, server error shown, change, normal app). Not tested: real Windows installer, a real Docker install of the new image, SSO users, upgrade of a real old install.
