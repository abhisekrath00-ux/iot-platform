# Competitive gap analysis

Compared against public documentation of ThingsBoard, AWS IoT, Losant and
Ignition (sources at the end). "Have" means implemented and tested in this
repo; "Partial" means a real but narrower version; "Missing" means not built.
This is a product-judgment document, not a marketing claim: rows are marked
from the code, not from intent.

| Capability | Reference platform | This product | Status |
|---|---|---|---|
| Multi-tenancy, tenant-scoped data | ThingsBoard | Tenant id on every query, integration-tested isolation | Have |
| RBAC, SSO, audit log | ThingsBoard, Ignition | Roles, OIDC, audit table and page | Have |
| Device authentication by X.509 | ThingsBoard, AWS | mTLS per gateway, broker ACLs from cert CN | Have (gateways); direct MCU devices only designed |
| Device profiles | ThingsBoard | Profiles with per-protocol point maps, drive generated edge config | Have |
| Industrial protocols | ThingsBoard IoT Gateway | Modbus RTU/TCP, OPC UA (secure modes), serial JSON | Partial (no BACnet, CAN, SNMP, LwM2M, CoAP) |
| Store and forward at the edge | Ignition | SQLite queue, delete after broker ACK | Have |
| Fleet updates with staged rollout | AWS Device Management | Staged cohorts, rollback, ack | Have (edge agent config/release); no signed firmware for end devices |
| Dashboards and widgets | ThingsBoard (300+ widgets) | 5 widget types with thresholds, drag-drop layout and sizing, wall mode with rotation, product tour, light/dark glass UI | Partial (small widget library; drag/wall covered by a manual browser e2e, not CI) |
| Rule engine / workflows | ThingsBoard, Losant | Threshold rules, flows (trigger, condition, delay, notify) with versions and simulator | Partial (no scripting node, no device-state write node; cooldown and latch exist) |
| Alarm lifecycle (ack, clear, assign) | ThingsBoard | Open, acknowledged, resolved with notes and history in API and UI | Have (no assignment or escalation) |
| Notifications | ThingsBoard | SMTP and Slack, scheduled reports | Partial (no SMS, Teams, escalation chains) |
| Reports, scheduled delivery | Ignition, ThingsBoard | Builder with preview, 15min-week buckets, CSV/HTML, cron delivery | Have |
| Digital twin: assets, hierarchy, relations | ThingsBoard, Azure | Site, gateway, device plus a per-device twin card with health score (2.5D SVG, not 3D) | Partial (no asset hierarchy or relations) |
| Device attributes, tags, groups, fleet search | AWS, ThingsBoard | Tags, fleet search and filters | Partial (tags only; no groups or typed attributes) |
| Desired/reported state (shadow, twin) | AWS, Azure | None | Missing |
| Data retention and downsampling | Ignition (QuestDB historian), ThingsBoard | Hourly rollups, optional raw purge, reports read rollups for purged ranges | Partial (no daily rollups, no per-tenant retention; 24h series API still reads raw) |
| API keys / service tokens | all | Hashed, expiring, revocable keys; viewer/operator role; endpoint-group scopes; per-key rate limit; never able to approve commands; audited | Have (coarse scopes, per-replica limit) |
| Secrets management | Ignition 8.3 | Env vars and vault-free config | Missing |
| Edge rules, edge compute | Losant, ThingsBoard Edge | Allowlisted commands, edge gate (expiry, replay, TTL), ack path, simulated actuator only | Partial (no real actuator drivers, by design until hazard analysis) |
| Mobile app | ThingsBoard | Responsive web only | Missing |
| Bulk device provisioning | AWS | One device at a time via onboarding | Missing |
| AI access | none of the above natively | MCP server, 7 read-only tools | Have |
| Air-gapped deploy | ThingsBoard (self-host) | Offline bundle, self-hosted assets, verified install scripts | Have (dry-run on a clean VM still pending) |
| Read caching, HA | ThingsBoard microservices | Per-replica response cache, leader election for scheduler | Partial |

## Ranked build list

Status as of 2026-10-01 morning: items 1 to 7 below are built (see the table for caveats). Remaining gaps are the table rows still marked Partial or Missing.

Chosen for customer impact on the stated use (energy meters, door sensors,
STM32 and PLC sources, enterprise monitoring) and for low risk:

1. Alarm lifecycle: acknowledge, clear, comment, history. Without it the
   alert page is a log, not an operations tool.
2. Device attributes and tags plus fleet search and filters. Everything else
   (groups, dashboards per group, bulk actions) depends on it.
3. Data retention policy with downsampled rollups, so long-running sites do
   not degrade.
4. API keys for integrations (hashed, scoped, revocable, audited). Needed for
   SCADA and BI pulls without sharing user logins.
5. Digital twin page per device (health, freshness, quality, attributes).
   Assets and relations come after attributes.
6. Wall/TV mode with drag-and-drop layout and a larger widget library.
7. Latch/dedupe node in flows to stop repeat alerts.

Not planned without owner input: SMS/Teams channels (external accounts),
mobile app, BACnet/LwM2M/CoAP (need hardware or a customer requirement),
firmware signing for end devices.

## Sources (fetched 2026-10-01)

- ThingsBoard, Why ThingsBoard: https://thingsboard.io/docs/pe/why-thingsboard/
- AWS IoT Device Management features: https://aws.amazon.com/iot-device-management/features/
- Losant workflows: https://docs.losant.com/workflows/overview/
- Ignition introduction (8.3): https://www.docs.inductiveautomation.com/docs/8.3/getting-started/introducing-ignition
