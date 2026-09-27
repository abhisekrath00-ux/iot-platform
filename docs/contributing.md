# Contributing

## Workflow

1. Pick an issue (GitHub Projects board: Backlog / Ready / In progress / Review / Done).
2. Branch from `main`: `feat/<short-name>`, `fix/<short-name>`, `docs/<short-name>`.
3. Small, reviewable PRs. Include tests; every fix starts with a failing test.
4. PR template: what, why, how tested, risk, rollback. Link the issue.
5. One approving review required; safety-relevant changes (command path, auth,
   drivers) require review from someone other than the author with domain
   knowledge. CI must be green.

## Code standards

- Go: `gofmt`, `go vet`, meaningful errors, no panics in library code, context
  propagation, structured logs (no secrets in logs).
- TypeScript: strict mode, ESLint clean, no `any` without a comment.
- Migrations: idempotent, backward compatible within a release pair, tested
  against a production-shaped dataset before release.
- Commits: imperative subject, body for the "why". Reference issues.

## Definition of done

Code + tests + docs updated; dashboards/alerts for new operational behavior;
security checklist considered (docs/security.md); migration rollback verified
if schema changed.

## Reporting vulnerabilities

Do not open public issues. Email the maintainer privately; include repro and
affected versions. We acknowledge within 2 business days.
