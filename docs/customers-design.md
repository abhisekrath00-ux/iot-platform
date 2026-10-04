# Customer hierarchy: design and security review

Status: built, tested against a real Postgres (leak tests below). Not remote-CI verified. Not used by any real customer.

## Model
- The **tenant** stays the hard isolation boundary (every table carries `tenant_id`). Customers live inside a tenant.
- `customers(id, tenant_id, parent_id, name)` forms a tree, depth at most 6, name unique per parent.
- `devices.customer_id` (nullable): a device belongs to at most one customer. No customer means tenant-level only.
- `user_customer_scope(tenant_id, user_id, customer_id)`: at most one row per user. A user without a row is unscoped (tenant-wide, as before). Keyed by the login subject, so it works for local, OIDC and API-key principals alike.
- A scoped user sees devices whose customer is the scoped customer or any descendant.

## Enforcement: default deny
One middleware (`customerScope`, `cmd/api/customers.go`) wraps the whole authenticated API, including calls the AI assistant makes for a user.
1. Look up the caller's scope. A lookup error returns 503 (fail closed, never "no scope").
2. Unscoped: pass through unchanged.
3. Scoped: only the allowlist below passes, GET only. Everything else is 403, including every write and every admin screen, whatever the user's role.
4. Allowlist: `GET /v1/devices`, `/v1/alerts`, `/v1/alerts/{id}`, `/v1/devices/{id}/health`, `/v1/telemetry/{latest,series,count,rollup,anomalies,forecast,related}`, `/v1/features`, `/v1/map/config`.
5. Per-request device checks: telemetry needs `device_id`, and the device (or the alert's device) must be in the subtree, else 404 (not 403, so existence does not leak). List endpoints filter in SQL by the subtree.
6. The response cache is bypassed for scoped requests, so a scoped answer is never stored in or served from the shared tenant cache.

A new endpoint is therefore invisible to scoped users until someone adds it to `scopedAllows`.

## Management
Admin session only (not API keys, not the assistant): create/delete customers, assign a device, scope or unscope a user. All audited. Deleting needs the customer empty. Parent must be in the same tenant.

## Threats considered
| Threat | Control |
| --- | --- |
| Scoped user reads another customer's device by id | per-request device check, 404 |
| Scoped user lists devices/alerts across customers | SQL subtree filter |
| Shared response cache serves one view to another user | cache bypass for scoped requests |
| New endpoint forgets the scope | default deny allowlist, plus a test that parses the route table |
| Scope lookup fails open | 503 on error |
| Scoped admin role escalates | allowlist ignores role, writes refused |
| API key or assistant widens scope | customer management refuses key/assistant calls; the middleware also applies to them |
| Cross-tenant parent/customer id | all customer queries carry `tenant_id`; 404 |

## Tests
`TestIntegrationCustomerScope` (list, alert, health, telemetry, rollup, cross-tenant, writes, key refusal, unscope) and `TestScopedUsersAreDeniedEveryUnlistedRoute` (every registered route outside the allowlist is refused). Sabotaging the device-list filter makes the first test fail.

## Not covered (honest limits)
- Scoped users are read-only and see devices, values and alerts, plus dashboards and reports an admin has shared with their customer. No flows or assets per customer.
- Sharing: `PUT /v1/dashboards/{id}/customer` and `PUT /v1/reports/{id}/customer` (admin session only, never an API key or the assistant). The server refuses to share, and refuses later edits of, a dashboard layout or report definition that names a device outside the customer subtree (409).
- A scoped user may GET `/v1/dashboards`, GET `/v1/reports` and download a shared report. The download refuses any run-time parameter except `format` and `theme` (so no `device=` override), and the report list hides schedule and delivery channel. Preview, run, versions, edit and export stay denied.
- Tested by `dashscope_test.go` and `reportscope_test.go` (cross-customer, cross-tenant, unshare, sabotage-checked).
- Rows are not tagged by customer for alerts without a device, so those are hidden from scoped users.
- No per-customer branding, quotas or billing. One scope per user.
- Real-time streams and exports are not on the allowlist.
- Next: the multi-user SaaS layer (tenant signup, user management, roles per customer, per-tenant logs) builds on this model; see the roadmap in competitive-gap.md.
