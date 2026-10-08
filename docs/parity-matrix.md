# Parity matrix per product

One place to see, per product, what is built, partial or not built. No product is at parity. Every row is taken from `docs/competitive-gap.md` (rows marked from the code, not intent) or `docs/report-builder-parity.md`; follow the link for the evidence and test status. Siemens, ThingsBoard and Node-RED comparisons use their public documentation, not hands-on use. Last reviewed 2026-10-08.

Status words: Built (implemented and tested here), Partial (narrower version), Not built.

## ThingsBoard

| Area | Status | Gap |
|---|---|---|
| Multi-tenancy, RBAC, audit, OIDC SSO | Built | SAML only through an OIDC bridge, not tested here |
| Device profiles, bulk provisioning, X.509 gateways | Built | Direct MCU devices only designed |
| Alarm lifecycle (ack, assign, escalate) | Built | No SMS or Teams notifications |
| Industrial protocols and gateway | Partial | Many protocols simulator-tested only; no real hardware runs |
| Dashboards and widgets | Partial | 8 widget types against ThingsBoard's 300+ |
| Rule engine | Partial | Flows and threshold rules, no full node library |
| Device groups, typed attributes | Partial | Tags only |
| Maps and geofencing | Partial | Circular zones; no polygons, enter/exit alerts or live GPS |
| OTA firmware for end devices | Partial | Edge agent releases only |
| White-label | Partial | Name and accent colour; no logo upload |
| Mobile app | Partial | Responsive web and PWA; no native app |
| Microservice-style clustering | Partial | Scheduler leader election, per-replica caches; 3-node HA generator never run on real machines (docs/ha-multi-node.md) |

## Siemens Insights Hub

| Area | Status | Gap |
|---|---|---|
| Monitor: rules by asset type, KPIs from formulas | Partial | Arithmetic, six numeric functions (abs, round, sqrt, min, max, clamp) and if(a > b and not c < d, x, y) with and/or/not; no text functions |
| Asset Manager: hierarchy, aspects, files | Partial | Tree and device attach; limited aspects |
| Predict: anomaly detection | Partial | Median/MAD outliers only; no trained models |
| Visual Flow Creator style flows | Partial | See Node-RED |
| Edge analytics | Partial | Allowlisted commands and simulated actuator only, by design (hazard analysis) |
| Integration and data exchange | Partial | Webhook, Kafka, AMQP, HTTP ingest; no Insights Hub connector |
| Fleet and OTA | Partial | Staged rollout with signed manifests, unit-tested on the edge only |

## Node-RED

| Area | Status | Gap |
|---|---|---|
| Flow canvas with function, switch, change, debug, inject, HTTP request | Built | Node library is small against Node-RED's |
| Flow import/export JSON | Built | Own `hexmon-flow/1` format, not Node-RED JSON |
| Sandboxed function node | Built | JS only |
| Custom node SDK | Partial | Saved function presets; no SDK or typed parameters |
| Community palette | Not built | Air-gapped: no package registry by design |

## Microsoft Report Builder

Full row-by-row matrix in `docs/report-builder-parity.md`. Summary: exports PDF, XLSX, CSV, HTML built, XML and Word partial; charts line, area, bar, scatter, gauge, pie (HTML only) partial; row groups, parameters, expressions, conditional formatting, page layout partial; list region, maps, stacked/combo charts, subreports, drill-through, Word, PowerPoint and a drag-drop designer not built.

## What this document does not show

Real-hardware runs, Windows runs and load tests are mostly absent; see `docs/status.md` for tested vs untested per feature. Remote CI has been unavailable, so nothing here is claimed as green there.
