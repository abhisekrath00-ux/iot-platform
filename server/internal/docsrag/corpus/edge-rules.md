# Edge-local alarm rules (siren / buzzer)

Rules that run on the edge box itself. They keep working when the server, broker or network is down, including when
the box boots with no connection. Rules are written on the server (Settings > Edge alarm rules) and shipped inside
the gateway's config, or added by hand on the box in `local-rules.yaml`.

## What it can and cannot do

- Drives **annunciator outputs only**: every output must be listed in the gateway's `outputs:` allowlist with
  `class: alarm`. A rule that names any other output is refused. Anything that moves process equipment (pumps, valves,
  breakers) stays on the approval + four-eyes command path (`docs/hazard-analysis.md`); this is not a control system.
- Rule types: `link_down` (broker connection lost for `for`), `threshold` (device point compared with a value, optionally
  held for `for`), `stale` (device not read successfully for `for`, at least 5s).
- Output kinds: `gpio_file` (writes 1/0 to an absolute path such as `/sys/class/gpio/gpio17/value` or an LED file, with
  `active_low`), `modbus_coil` (a write point of a device whose `writes` allowlist has it with min 0 and max 1, so it
  goes through the same range-checked writer as commands), `simulate` (log only).
- Safety rails: each output has `max_on` (default 10 min, max 1 h) after which it goes quiet until the alarm clears and
  fires again; all outputs are switched off when the agent stops; setter failures are retried every second and shown in
  status; invalid rules stop the agent at boot (a silently disabled alarm is worse than a loud failure); a pushed
  config with invalid rules is refused before it is installed.
- Local control: `edge-agent -silence siren -for 10m` (up to 24h) mutes an output; `edge-agent -test-output siren`
  sounds it for 5s. Both drop a small control file in the agent data directory, so they need write access to that
  directory. Rules keep evaluating while silenced and the siren resumes if the alarm is still active.

## Config shape

```yaml
outputs:
  - {name: siren, class: alarm, kind: gpio_file, path: /sys/class/gpio/gpio17/value, max_on: 5m}
rules:
  - {id: server-lost, type: link_down, for: 30s, output: siren}
  - {id: boiler-hot, type: threshold, device: boiler, point: temp, op: ">", value: 90, for: 10s, output: siren, pattern: pulse}
  - {id: meter-dead, type: stale, device: meter1, for: 60s, output: siren}
```

`local-rules.yaml` (next to the main config) holds only `rules:`; it cannot add outputs, and its rule ids must not
repeat a pushed id.

## Honest status

- Built and tested: rule engine (hold times, max-on cap, silence, test, pulse, retry, shutdown-off), validation, GPIO-file
  and coil setters (with fakes), an end-to-end offline-siren test with no broker, server storage/validation/YAML
  rendering, and the Settings editor.
- **Not tested on real hardware**: no real GPIO, relay board or siren has been driven. Check the wiring, the path and the
  polarity (`active_low`) with `-test-output` before relying on it.
- Link detection means the **MQTT broker connection**. If the broker is up but the API or database is down, the box
  cannot tell. A broker on the same host as the server is the usual layout, so this covers a server or network outage.
- Pushing: saving rules updates the gateway's config download; to apply, download it again (or push it as a fleet config
  campaign) and restart the agent. A one-click live push is not built. Fleet config applies restart the rules engine.
- The server editor offers `gpio_file` and `simulate`; `modbus_coil` outputs are set up in the box's config file.
- No serial-relay output, no sound-file playback (a "siren" here is a switched output), no escalation or acknowledgement
  workflow, and edge rule activations are not reported back to the server yet.
