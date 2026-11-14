# Edge Modbus explorer

Status: first implementation, local/synthetic acceptance only. Not an owner update or a finished ModScan replacement. The existing commissioning mapper is still available independently.

## What exists

The edge agent can serve a separate interactive explorer when explicitly started with `-explorer-listen 127.0.0.1:8089`. It is disabled by default. This is an engineering entry point, not yet the nontechnical desktop launch flow. No existing installation, service arguments, source configuration or identity is changed by this work.

The explorer lists Modbus connections that the polling supervisor already owns. Selecting one does not open a second serial handle or dial another TCP connection. A selected connection's transport, endpoint and unit are fixed. Each driver lifetime has a new opaque ID. Closing/reloading a driver expires its ID rather than silently routing an old selection to the replacement.

The browser supports:

- FC1 coils, FC2 discrete inputs, FC3 holding registers and FC4 input registers, explicitly selected by the operator.
- Zero-based wire offsets or one-based addresses. One-based address 1 means wire offset 0. These are not 40001-style reference numbers. The selected range must fit wire offsets 0-65535 and contain 1-32 addresses.
- Start/Stop ongoing reads, a 1000-60000 ms interval, attempts, valid responses, errors, latency and last-valid time. Only one read is pending. Slow responses do not create a timer queue. A busy driver skips a sample rather than waiting behind telemetry or a command.
- Raw decimal/hex register words or bits, independent names and units, scale, u16/i16/u32/i32/f32/f64 decoding and abcd/badc/cdab/dcba order. Multi-word decoders need the full number of words in the selected range. Non-finite results are not presented as measurements.
- Tab-local address labels that survive samples and same-lifetime range changes, with a 512-entry bound and bounded JSON import/export. Imported strings are text, never markup. Label-only imports retain original connection-lifetime IDs; they do not retarget to a new device automatically.
- A separate portable source-template draft. Stop reading, select a point's starting wire address, enter its stable ID and accepted limits, and include a copy of its reviewed label/decoder. The draft rejects duplicate IDs, same-function word overlap, incomplete/out-of-range samples, zero/nonfinite scale, mixing connection lifetimes and unsupported Dashboard profile types such as f64. At most 64 points. A template name and exact-model manual reference are required for the JSON preview; metadata/point changes invalidate review. Export needs an explicit review checkbox. The file contains no host, serial port, unit address, gateway identity, command or write grant.
- Dashboard Profiles recognizes the versioned portable template, loads its name/driver/points into a draft, and preserves friendly names and manual descriptions. Unsupported metadata, connection/write fields, malformed rows and unsupported types do not replace the current map. A separate Create profile button is still required; source assignment and Apply are separate existing steps. Export/import never creates a profile automatically.
- Stale values on Stop/error, plus a stale-age watchdog. Stop cancels the request and prevents another sample; it cannot recall bytes already sent to a device. The underlying transaction keeps its finite driver I/O timeout and holds the one-pending fence until it returns.
- Desktop table and narrow-screen address cards, with labels, raw words and decoded values visible without page-level horizontal overflow. Same-range restart/error keeps last-known rows and editable labels visibly stale; a genuinely new range has editable Not sampled rows and resets range counters/time. A restart no longer clears values while retaining old counters.

Viewing the page, refreshing the connection list, editing labels and importing/exporting labels do not read a meter. Start initiates reads. This tool has no write endpoint, raw frame operation, arbitrary function code, automatic source apply or clock-setting action.

## Ownership and local request boundary

`driver.RangeReader` borrows an already-open generic Modbus RTU/TCP driver and uses its transaction mutex with TryLock. It permits only FC1-4, validates the wire range before I/O and validates exact response length before exposing words. It never performs an internal automatic retry. RTU response reads assemble bounded fragments, verify unit/function/count/CRC, handle exception PDUs and fail on zero progress or a short write without resending. TCP response reads verify transaction/protocol/unit/function/count and reject malformed/truncated exceptions without a panic. Driver Close drains a transaction and prevents a stale reference from reopening it. The registry detaches/drains a source before its owner closes the driver.

The interactive handler is not attached to the existing configurable LAN status page. Its separate listener requires a literal loopback IP, an exact Host match, a loopback peer, matching Origin for reads, and a random per-server session header. Cross-site and same-site-but-not-same-origin fetch metadata is refused. The session is delivered only by the no-store local page, not a query string. There is no CORS grant. Framing is disabled by CSP. JSON read bodies are bounded, reject unknown fields and accept exactly one object. Concurrent reads receive a busy response without touching the source. Request correlation, exact count, bit values and cancellation are checked before returning values.

This does not protect against a hostile program running as the same local user. It also does not supply cross-process ownership for unrelated third-party tools. New/unconfigured-port exploration remains unwired until its ownership path exists. This slice does not widen the existing remote commissioning or discovery APIs.

## Acceptance performed

- Three race-enabled repetitions of the explorer, driver and edge-agent packages, plus full edge tests and vet.
- Synthetic supervisor lifetime registration, expiry and no read on inventory.
- Loopback/Host/Origin/session/method/content-type/body/bounds guards before any source call; one-pending no queue; malformed bit/count/cancelled responses never published as values.
- Driver invalid-function/bounds checks before any wire call, exact response counts, busy skip, close/read drain and refusal of stale readers.
- Ten race-enabled driver-suite repetitions including single-byte RTU/TCP fragmentation, packed coils/discrete bits, unit/function/count/CRC/protocol/transaction mismatches, malformed/truncated exceptions, zero-progress response and partial-write refusal. No frame retries. Windows amd64 cross-build passes; this is not Windows runtime acceptance.
- Real Chromium at 1440 and 390 CSS pixels against the actual handler and a synthetic source: no initial meter read, ongoing reads, no post-Stop request, delayed responses longer than the interval without overlap, error/recovery, names/units/decoder retained, float decode 100, label export/import and no document overflow. Actual pixels were inspected after fixing a form-focus/redraw bug that had dropped an edited unit, and a same-range restart/error bug that had hidden stale rows while retaining old counters. The corrected error-state regression asserts visible labels, units, decoder and stale value 100, not only an error message.

- Real Chromium desktop/mobile portable-template path: explicit point inclusion, duplicate/overlap refusal, metadata change invalidates review, reviewed JSON download, actual Dashboard draft import with manual/names/decoder preserved, no automatic save, then exactly one explicit synthetic profile save and no source Apply/write. Preview/editor and Create profile areas inspected as pixels.
- The actual browser-exported synthetic JSON is a checked-in API test fixture. An isolated-PostgreSQL API test verifies profile name/driver/point/manual/limits round-trip and unchanged device count.

No physical serial adapter or meter, owner PC, production gateway, Windows service, desktop launch or real clock-setting procedure was exercised.

## Still open before the joint product handoff

- Nontechnical desktop entry and controlled local listener lifecycle.
- Adding or selecting an unconfigured RTU/TCP connection with explicit serial ownership. Do not add an unrestricted probe API or silently pause the service.
- Completing the portable-profile-to-source selection/assignment/Apply path as one guided workflow rather than a file handoff, and its owner-facing source-list/receipt acceptance. Label-only JSON remains distinct from the new reviewed portable profile JSON. Neither changes telemetry by export/import.
- Physical USB/COM and model-specific register/address-order/timing acceptance. The synthetic fragmented-read matrix is not a physical-device result.
- Supported exact-model time sync as a separate confirmed operation, if a documented procedure is actually supported. The supplied meter family manual has not established that support. No arbitrary register write or physical write is approved by the explorer build request.
- Existing runtime durability gaps, staged schema/update compatibility and the combined secure-server update/fresh dashboard EXE acceptance remain independent open work. Nothing here changes those claims.

## Behavioral reference

The user selected OpenModScan as a behavioral/protocol-reading reference, not a bundling or fork decision. The examined source separates address descriptions from values, uses explicit function/address/count selections and does not queue another poll while one reply is pending:

- https://github.com/sanny32/OpenModScan
- https://github.com/sanny32/OpenModScan/blob/main/src/formmodsca.cpp
- https://github.com/sanny32/OpenModScan/blob/main/src/formmodsca.h
- https://github.com/sanny32/OpenModScan/blob/main/src/modbusclient.cpp

This implementation does not bundle OpenModScan, its binary, Qt, or its source. It does not promise full feature parity.
