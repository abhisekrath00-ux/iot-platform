# ABB M4M 30 (three-phase network analyzer)

Sources and provenance, in two honestly separated classes:

1. Directly supported by ABB's official "MODBUS MANUAL M4M Network analyzers"
   (library.e.abb.com, "M4M Modbus communication.pdf", fetched 2026-10-20): measurements
   are read with function code 03 (read holding registers) - NOT FC 04 - and the manual's
   worked example reads phase voltage L1 from register 0x5B02 with 2 registers. The manual
   also confirms the M4M family offers both Modbus RTU (RS-485) and Modbus TCP variants.
2. From ABB's separate "M4M Modbus protocol map" workbook 9AKK107492A7627, map v1.3O
   (rev. 2023-10-23), which covers the M4M 20 / M4M 30 family (all variants; tariff energy,
   CT neutral ratio and unbalance exist only on M4M 30; some registers need firmware
   P1.3.10.0 or later) - as re-typed by the third-party mirror
   easymodbus.com/modbus-register-maps/abb/m4m-20-30 (fetched 2026-10-20), NOT re-verified
   line-by-line against ABB's own file: every address other than 0x5B02, every scale
   (x0.1 V; x0.01 A/W/var/VA/Hz; x0.001 PF) and the ABCD high-word-first word order.
   The mirror's L1-voltage row (decimal 23298 = 0x5B02) agrees with the class-1 anchor.

Nothing here has been run against a real meter. The template's installer confirmation is
an acknowledgement of this split, not a verification: exclude or correct any point whose
values disagree with the authoritative documentation for the exact model and firmware in
front of you.

## Link

Modbus RTU over RS-485, or Modbus TCP on the Ethernet variants; the map is the same.
Function code 03 (read holding registers) - NOT FC 04. Addresses are 0-based wire
addresses, sent unchanged.

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

## Decode fixtures

edge/internal/driver/modbus_m4m_test.go proves the edge decode path for the template's
formats over FC 03: u32 x0.1, u32 x0.01, i32 x0.01 (including a negative export-power
value), u16 x0.01, i16 x0.001 (including a negative PF), ABCD word order, and asserts the
poller actually emits function code 0x03. Synthetic registers only - a decode-format proof,
not a hardware claim.
