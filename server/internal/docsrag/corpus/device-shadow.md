# Device reported state and attributes

Reported state only. There is no desired state: nothing here is ever sent to a device, and commands stay on the
approval + four-eyes path (`docs/hazard-analysis.md`).

- `GET /v1/devices/{id}/shadow` (any role): the newest reading per point from the last 7 days (value, unit, quality,
  `observed_at`, `age_seconds`), `last_seen`, and the device's attributes. A reading is `stale` when it is older than
  three polling intervals (30 s minimum); with no interval configured, older than 15 minutes.
- `PUT /v1/devices/{id}/attributes` (admin, operator): replaces the attributes. At most 32 names (letter first, then
  letters, digits, `.` `_` `-`, 40 chars); values are text (256 chars), numbers or true/false. No nesting. Audited as
  `device.attributes`. Stored in `devices.attributes` (migration 0031).
- Device page: a "stale" mark on each reading card and an Attributes card (name=value lines).

Tested: unit tests (validation, stale rule, the text parser in the UI) and an integration test (newest reading wins,
stale flag, role gate, validation, tenant isolation). Not built: desired state, delta notifications, attributes pushed to
devices, attribute search or use in rules and reports.
