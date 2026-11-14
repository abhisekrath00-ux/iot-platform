# Edge Modbus explorer

Status: first implementation, local/synthetic acceptance only. Not an owner update or a finished ModScan replacement. The existing commissioning mapper is still available independently.

## What exists

The edge agent can serve a separate interactive explorer when explicitly started with `-explorer-listen 127.0.0.1:8089`. It is disabled by default. This engineering flag is optional. The desktop manager now has explicit Open device explorer and Close device explorer actions for an already-running service. Open requests an ephemeral literal-loopback listener from that same process; it does not restart the service or change its arguments. The listener closes after one hour unless the operator explicitly opens it again. Close drains its reads and closes temporary connections, not existing telemetry drivers. No source configuration or identity is changed. Windows GUI pixels and native service execution of these new actions remain unaccepted; only the PowerShell contracts, real local listener/control tests and cross-build have run.

The explorer lists Modbus connections that the polling supervisor already owns. Selecting one does not open a second serial handle or dial another TCP connection. A selected connection's transport, endpoint and unit are fixed. Each driver lifetime has a new opaque ID. Closing/reloading a driver expires its ID rather than silently routing an old selection to the replacement.

The browser supports:

- Explicit Open and Disconnect for one temporary, unconfigured RTU/TCP connection. RTU needs the exact port, baud, 8 data bits, parity, stop bits and unit. TCP needs a literal unicast IP, port and unit. Mixed transport fields are refused. Opening serial configures local line framing and may change adapter line state; it sends no Modbus request. TCP is lazy and does not dial before Start. Temporary connections are never persisted or applied, expire after 10 idle minutes or 30 total minutes, and close with the listener. Stop leaves the connection open; Disconnect closes it. Expired IDs stop browser sampling without reconnecting automatically.

- FC1 coils, FC2 discrete inputs, FC3 holding registers and FC4 input registers, explicitly selected by the operator.
- Zero-based wire offsets or one-based addresses. One-based address 1 means wire offset 0. These are not 40001-style reference numbers. The selected range must fit wire offsets 0-65535 and contain 1-32 addresses.
- Start/Stop ongoing reads, a 1000-60000 ms interval, attempts, valid responses, errors, latency and last-valid time. Only one read is pending. Slow responses do not create a timer queue. A busy driver skips a sample rather than waiting behind telemetry or a command.
- Raw decimal/hex register words or bits, independent names and units, scale, u16/i16/u32/i32/f32/f64 decoding and abcd/badc/cdab/dcba order. Multi-word decoders need the full number of words in the selected range. Non-finite results are not presented as measurements.
- Tab-local address labels that survive samples and same-lifetime range changes, with a 512-entry bound and bounded JSON import/export. Imported strings are text, never markup. Label-only imports retain original connection-lifetime IDs; they do not retarget to a new device automatically.
- A separate portable source-template draft. Stop reading, select a point's starting wire address, enter its stable ID and accepted limits, and include a copy of its reviewed label/decoder. The draft rejects duplicate IDs, same-function word overlap, incomplete/out-of-range samples, zero/nonfinite scale, mixing connection lifetimes and unsupported Dashboard profile types such as f64. At most 64 points. A template name and exact-model manual reference are required for the JSON preview; metadata/point changes invalidate review. Export needs an explicit review checkbox. The file contains no host, serial port, unit address, gateway identity, command or write grant.
- Dashboard Profiles recognizes the versioned portable template, loads its name/driver/points into a draft, and preserves friendly names and manual descriptions. Unsupported metadata, connection/write fields, malformed rows and unsupported types do not replace the current map. A separate Create profile button is still required; source assignment and Apply are separate existing steps. Export/import never creates a profile automatically.
- Stale values on Stop/error, plus a stale-age watchdog. Stop cancels the request and prevents another sample; it cannot recall bytes already sent to a device. The underlying transaction keeps its finite driver I/O timeout and holds the one-pending fence until it returns.
- Desktop table and narrow-screen address cards, with labels, raw words and decoded values visible without page-level horizontal overflow. Same-range restart/error keeps last-known rows and editable labels visibly stale; a genuinely new range has editable Not sampled rows and resets range counters/time. A restart no longer clears values while retaining old counters.

A displayed range keeps its source/transport/endpoint/unit/function/wire context even after a disconnect.

Viewing the page, refreshing the connection list, editing labels and importing/exporting labels do not read a meter. Start initiates reads. This tool has no write endpoint, raw frame operation, arbitrary function code, automatic source apply or clock-setting action.

## Guided saved-source handoff

In Dashboard Add device, resume the saved gateway setup and choose **Use a template from the edge explorer**. Import the exported JSON. Review the manual reference, each point's wire offset/function, type/order, scale/unit and accepted limits before **Save and select imported template**. This saves one reusable profile. It does not send a probe or assign/apply anything. Connection identity is not portable: the serial port/host/unit remain blank for explicit entry.

Enter the source name and actual gateway connection, then **Assign profile**. This saves the source in the commissioning session. The saved source remains visible after reloading that session. A lost or malformed assignment receipt stops further assignment and asks you to reload the saved setup; it never automatically repeats the mutation. An uncertain profile save similarly directs you to check Profiles before creating another.

Review the full saved source list separately, then **Apply saved source settings**. Review is tied to this gateway, source list and desired configuration fingerprint; a changed list invalidates it. A mismatched/lost Apply receipt stays unconfirmed, with no automatic retry. Sent, applied and confirmed are different states. Applied means the gateway saved settings, not that reload or readings succeeded. Even matching confirmation still requires a recent measured source reading. **Check live source readings without a probe** only reads server-side preview. Historical, estimated or stale data does not complete setup. Close a temporary explorer serial connection before applying a saved source using that port; the application does not secretly pause another owner.

These controls do not enable signed source updates, provision secrets, activate a deployment, authorize physical writes or certify device calibration. The separate advanced config download/import remains available but downloading a file does not start source polling.

## Ownership and local request boundary

`driver.RangeReader` borrows an already-open generic Modbus RTU/TCP driver and uses its transaction mutex with TryLock. It permits only FC1-4, validates the wire range before I/O and validates exact response length before exposing words. It never performs an internal automatic retry. RTU response reads assemble bounded fragments, verify unit/function/count/CRC, handle exception PDUs and fail on zero progress or a short write without resending. TCP response reads verify transaction/protocol/unit/function/count and reject malformed/truncated exceptions without a panic. Driver Close drains a transaction and prevents a stale reference from reopening it. The registry detaches/drains a source before its owner closes the driver.

The interactive handler is not attached to the existing configurable LAN status page. Its separate listener requires a literal loopback IP, an exact Host match, a loopback peer, matching Origin for reads, and a random per-server session header. Cross-site and same-site-but-not-same-origin fetch metadata is refused. The session is delivered only by the no-store local page, not a query string. There is no CORS grant. Framing is disabled by CSP. JSON read bodies are bounded, reject unknown fields and accept exactly one object. Concurrent reads receive a busy response without touching the source. Request correlation, exact count, bit values and cancellation are checked before returning values.

All production serial driver, probe and sweep opens use a shared reservation before the library can change framing. Unix identity is the canonical character-device ID, so symlink aliases share the in-process fence. A nonblocking device flock coordinates participating processes; Windows retains the serial library's exclusive CreateFile behavior. Failed opens release reservations; Close is once-only; another owner is refused rather than silently stopped. Unix programs that ignore advisory locks, privileged programs and older programs without a working exclusive lock are not reliably detectable. Close third-party serial tools yourself before opening a temporary RTU connection. This is a cooperative ownership guarantee, not a claim that arbitrary serial programs cannot interfere. Hardware/OS-driver acceptance remains open.

The desktop control uses fresh UUID requests and matching receipts in the local protected state directory, never an unauthenticated network control API. It marks an attempt processing durably before changing the listener. An interrupted processing receipt becomes visible uncertainty and is not replayed. Requests are bounded, strict, expire after 15 seconds, refuse concurrent CLI requests and have an eight-second confirmation limit without retry. The manager validates the returned literal 127.0.0.1 HTTP URL, request receipt and lifetime before opening a browser; a stopped service is refused without starting it. No listener is bound by the watcher alone. Unix receipts use file and directory sync; Windows replacement requests write-through. Native Windows durability/lifecycle acceptance is still required.

This does not protect against a hostile program running as the same local user. The existing optional LAN status, remote commissioning and discovery interfaces are not widened.

## Acceptance performed

- Guided browser flow at 1440/390 with the actual exported portable JSON: explicit reviewed profile save/select, blank endpoint/unit, explicit source assignment, changed-list review invalidation, one Apply, applied versus confirmed status, stale versus recent measured preview, saved-session recovery without repeated writes and lost-assignment quarantine. These use synthetic API fixtures; they are not physical or native Windows acceptance.
- Real profile API and isolated Postgres, compiled edge/ingest, synthetic read-only Modbus TCP peer and certificate-required broker: the export-derived template is saved, explicitly assigned, recovered from its saved session, duplicate assignment refused, signed config applied and matching fingerprint confirmed, then the actual measured Float32 100 reaches preview. Plaintext and mTLS variants pass locally with race detection. The existing mTLS CI entry now calls the source and portable-template regressions with an actual compiled edge; no workflow file changed. Remote acceptance still requires inspecting that exact commit's run.


- Twenty race-enabled serial-owner repetitions on synthetic PTYs: canonical aliases, competing probes, twenty concurrent contenders with exactly one winner, ordinary-file refusal, failed-open cleanup and a separate cooperating process whose crash releases its lock. These are virtual TTYs, not USB hardware.
- Temporary connection validation before opening, explicit single-session lifecycle, idle/hard expiry, in-flight refusal, borrowed-driver protection, close/shutdown, stale lifetime and protected connection API tests.
- Actual Chromium 1440/390 form tests over a real synthetic loopback TCP Modbus peer: opening does not dial/read, second-open refusal preserves selection, Start decodes 100, Stop/Disconnect, missing-port refusal, stale-ID rejection and terminal-expiry stop without reopen.
- PowerShell manager contracts with a fake agent: exact Open/Close flags and identity directory, receipt/local-URL/lifetime guards, no config change, stopped-service and native-error refusal. These ran with PowerShell on Linux, not WinForms or Windows SCM.
- `python scripts/test-edge-explorer-local.py` builds/runs the actual binary with disposable state, no devices and a reserved non-listening loopback broker port; its actual CLI Open/Close/reopen receipts and local HTTP page pass, with the running process/config/fixture marker retained. No installed service or real identity is used.
- Actual local controller -> receipt -> HTTP handler -> temporary connection -> Close -> reopen with a fresh capability -> agent cancellation, keeping borrowed drivers unread and attached. Processing-before-effect, completed/interrupted replay refusal, invalid/future/stale requests, CLI concurrency/timeout cleanup and abandoned-lock recovery are tested.
- Three race-enabled repetitions of the explorer, driver and edge-agent packages, plus full edge tests and vet.
- Synthetic supervisor lifetime registration, expiry and no read on inventory.
- Loopback/Host/Origin/session/method/content-type/body/bounds guards before any source call; one-pending no queue; malformed bit/count/cancelled responses never published as values.
- Driver invalid-function/bounds checks before any wire call, exact response counts, busy skip, close/read drain and refusal of stale readers.
- Ten race-enabled driver-suite repetitions including single-byte RTU/TCP fragmentation, packed coils/discrete bits, unit/function/count/CRC/protocol/transaction mismatches, malformed/truncated exceptions, zero-progress response and partial-write refusal. No frame retries. Windows amd64 cross-build passes; this is not Windows runtime acceptance.
- Real Chromium at 1440 and 390 CSS pixels against the actual handler and a synthetic source: no initial meter read, ongoing reads, no post-Stop request, delayed responses longer than the interval without overlap, error/recovery, names/units/decoder retained, float decode 100, label export/import and no document overflow. Actual pixels were inspected after fixing a form-focus/redraw bug that had dropped an edited unit, and a same-range restart/error bug that had hidden stale rows while retaining old counters. The corrected error-state regression asserts visible labels, units, decoder and stale value 100, not only an error message.

- Real Chromium desktop/mobile portable-template path: explicit point inclusion, duplicate/overlap refusal, metadata change invalidates review, reviewed JSON download, actual Dashboard draft import with manual/names/decoder preserved, no automatic save, then exactly one explicit synthetic profile save and no source Apply/write. Preview/editor and Create profile areas inspected as pixels.
- The actual browser-exported synthetic JSON is a checked-in API test fixture. An isolated-PostgreSQL API test verifies profile name/driver/point/manual/limits round-trip and unchanged device count.

No physical serial adapter or meter, owner PC, production gateway, Windows service/GUI launch or real clock-setting procedure was exercised. The new desktop entry has not been rendered or clicked on Windows.

## Still open before the joint product handoff

- Native Windows desktop Open/Close layout/click/service/lifetime acceptance. The entry/control code and portable contracts now exist, but native pixels and service execution are not accepted.
- Physical USB/COM acceptance of the new explicit temporary RTU/TCP path and cooperative ownership fence. No unrestricted probe API or silent service pause was added.
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
