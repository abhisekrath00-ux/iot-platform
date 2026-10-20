# Carlo Gavazzi EM340 (three-phase DIN-rail energy meter)

Source: the manufacturer's "EM300 Series Communication Protocol" document (CP), table 2.4-1
"Instantaneous variables and meters (grouped by variable type)" (gavazziautomation.com,
fetched 2026-10-20). Nothing here has been run against a real meter.

## Link

RS-485 / Modbus RTU (the protocol doc covers MODBUS/JBUS RTU; read-only with function codes
03 and 04, this template uses FC 04). Factory serial defaults per the doc: 9.6 kbps; address
and baud are set on the meter. Register numbers below are the doc's physical addresses (hex);
the doc's Modicon column is physical + 300001.

## Value formats

All 32-bit values are INT32 (signed) with documented "value weight" scaling: voltage
Volt*10 (scale 0.1), current Ampere*1000 (0.001), power Watt*10 (0.1), energy kWh*10 (0.1).
PF and frequency are INT16: PF*1000 (0.001; negative PF corresponds to exported active power
per the doc's note) and Hz*10 (0.1). Word order for 32-bit formats is LSW first (the doc
states "In INT32, UINT32 and UINT64 formats, the word order is LSW->MSW") = `cdab`.

## Measurements (read-only, FC 04)

| Point | Hex addr | Type | Scale | Unit |
|---|---|---|---|---|
| v1n, v2n, v3n | 0000-0004 | INT32 | 0.1 | V |
| v12, v23, v31 | 0006-000A | INT32 | 0.1 | V |
| i1, i2, i3 | 000C-0010 | INT32 | 0.001 | A |
| p1, p2, p3 | 0012-0016 | INT32 | 0.1 | W |
| s1, s2, s3 | 0018-001C | INT32 | 0.1 | VA |
| q1, q2, q3 | 001E-0022 | INT32 | 0.1 | var |
| v_sys_ln, v_sys_ll | 0024, 0026 | INT32 | 0.1 | V |
| p_sys, s_sys, q_sys | 0028, 002A, 002C | INT32 | 0.1 | W / VA / var |
| pf1, pf2, pf3, pf_sys | 002E-0031 | INT16 | 0.001 | - |
| phase_seq | 0032 | INT16 | 1 | - (1 = L1-L3-L2, 0 = L1-L2-L3) |
| freq | 0033 | INT16 | 0.1 | Hz |
| kwh_import, kvarh_import | 0034, 0036 | INT32 | 0.1 | kWh / kvarh |
| kw_dmd, kw_dmd_peak | 0038, 003A | INT32 | 0.1 | W |
| kwh_partial, kvarh_partial | 003C, 003E | INT32 | 0.1 | kWh / kvarh |
| kwh_l1, kwh_l2, kwh_l3 | 0040-0044 | INT32 | 0.1 | kWh |
| kwh_t1, kwh_t2 | 0046, 0048 | INT32 | 0.1 | kWh |
| kwh_export, kvarh_export | 004E, 0050 | INT32 | 0.1 | kWh / kvarh |

## Deliberately excluded

- Rows the doc marks "Not available, value = 0" on EM340: kWh (+) t3-t8, kVAh totals and
  partials, kWh (-) partials, kWh (-) L1-L3 (ET series only).
- Run hour meter (005A): documented for ET series and EM330 only.
- The integer/decimal-split energy representation at 301025+ (0400h): an alternative encoding
  of the same counters, redundant with the weighted INT32 block.
- Holding-register configuration (baud, address, CT ratios at 2001h+): writes are never
  inferred from a map; industrial onboarding is read-only by default.
