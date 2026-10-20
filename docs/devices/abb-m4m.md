# ABB M4M 30 (three-phase network analyzer)

Sources: ABB's official "MODBUS MANUAL M4M Network analyzers" (library.e.abb.com,
"M4M Modbus communication.pdf", fetched 2026-10-20) and the full Modbus table mirrored at
easymodbus.com/modbus-register-maps/abb/m4m-20-30 (fetched 2026-10-20), which documents
ABB's separate official Modbus table file with its decimal and hex addresses. Nothing here
has been run against a real meter.

## Link

Modbus RTU over RS-485 (M4M 30 also offers Modbus TCP over Ethernet; the map is the same).
Measurements are read with function code 03 (read holding registers) - NOT FC 04.
The official manual's worked example reads phase voltage L1 from register 0x5B02 with
2 registers; the mirrored table gives the same address (decimal 23298). That anchor is the
cross-check between the two sources.

## Value formats

Word order for 32/64-bit values is most-significant-register first (`abcd`). Voltages are
UINT32 x0.1 V; currents UINT32 x0.01 A; powers INT32 x0.01 W/var/VA; frequency UINT16
x0.01 Hz; power factor INT16 x0.001 (sign per quadrant). Energy counters are UINT64 x0.01
kWh/kvarh/kVAh.

## Measurements (read-only, FC 03)

| Point | Hex addr | Dec | Type | Scale | Unit |
|---|---|---|---|---|---|
| v_sys_ln | 5B00 | 23296 | UINT32 | 0.1 | V |
| v1n, v2n, v3n | 5B02-5B06 | 23298-23302 | UINT32 | 0.1 | V |
| v12, v23, v31 | 5B08-5B0C | 23304-23308 | UINT32 | 0.1 | V |
| i_sys, i1, i2, i3, i_n | 5B0E-5B16 | 23310-23318 | UINT32 | 0.01 | A |
| p_total, p1-p3 | 5B1A-5B20 | 23322-23328 | INT32 | 0.01 | W |
| q_total, q1-q3 | 5B22-5B28 | 23330-23336 | INT32 | 0.01 | var |
| s_total, s1-s3 | 5B2A-5B30 | 23338-23344 | INT32 | 0.01 | VA |
| freq | 5B32 | 23346 | UINT16 | 0.01 | Hz |
| pf_total, pf1-pf3 | 5B40-5B43 | 23360-23363 | INT16 | 0.001 | |

Vendor naming quirk: the line voltages are documented as L1-L2, L3-L2, L1-L3 (in that
order); the template keeps the v12/v23/v31 ids in address order.

## Excluded

Energy counters (UINT64 at 0x5000+, tariffs at 0x5170+, per-phase at 0x5460+): the edge
driver decodes up to 64-bit floats but has no u64/i64 integer type, so counters are not in
the template. Historicals (Energy Snapshots/Trend header+data-block protocol at 0x8000+),
alarms and I/O status are also out of scope.

## Provisional

The FC-03 function code and the L1-voltage address anchor are verified against the official
ABB manual. The per-quantity scales (x0.1 V, x0.01 A/W/var/VA/Hz, x0.001 PF) come from the
mirrored official table, not re-checked line-by-line against ABB's file. Confirm one live
voltage and one live power reading against the meter display before relying on values.
