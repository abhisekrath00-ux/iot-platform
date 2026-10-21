# Socomec DIRIS A-40, load 1 (three-phase panel meter)

Source: the manufacturer's "MODBUS tables of DIRIS A-40" communication table, document
CMT-1.7.4B EN on emea.socomec.com
(https://emea.socomec.com/sites/default/files/2025-01/DIRIS-A-40---MULTIFUNCTION-METER_COMMUNICATION-TABLE_2025-01_CMT-1.7.4B_EN_0.html).
First fetch 2026-10-20 was truncated before the energy sections; the full table (129k chars)
was re-read 2026-10-21 and the energy block verified line-by-line. The operating manual
545802B EN (emea.socomec.com) was also read: it confirms RS485 Modbus RTU is standard on the
base unit (9600-115200 baud, Ethernet variants add Modbus TCP port 502) but defers ALL
protocol detail to this same communication table. Nothing here has been run against a real
meter. The A-40 is a panel meter with three load channels; this profile reads the LOAD 1
instantaneous block plus the total energy counters.

## Conventions the vendor documents do NOT state (all gated in the template)

The communication table lists decimal address, hex address, word count, description, unit and
data type - and nothing else. Neither it nor the operating manual states:

- the multi-register WORD ORDER of U32/S32 values (32-bit values are two registers; which
  register is most significant is not stated),
- the READ FUNCTION CODE (FC 03 holding vs FC 04 input),
- the PDU BASE (whether the listed address or the listed address minus 1 goes on the wire).

The template therefore gates all three behind explicit installer choices and generates no YAML
until they are made. An earlier revision of this profile asserted "FC 04 input registers" and
"most significant register first"; those assertions were NOT vendor-grounded and have been
removed.

## Scaling (stated in the table's unit column)

Values are SIGNED and UNSIGNED INTEGERS with documented scaling, NOT floats: voltages in
V 10e-2 (scale 0.01), currents in mA (0.001), frequency in mHz (0.001), power factors in
1/1000 (0.001, signed). Powers are plain W / var / VA (scale 1). Energy counters are printed
as kWh (U32), "varh 10e3" (i.e. the register value is already in thousands of varh, so the
template labels it kvarh, scale 1) and kVAh (U32).

## Measurements (load 1 instantaneous block 0x4800+)

| Point | Dec address | Hex | Type | Scale | Unit |
|---|---|---|---|---|---|
| v_sys_ln, v_sys_ll | 18436, 18438 | 0x4804, 0x4806 | U32 | 0.01 | V |
| i_sys | 18440 | 0x4808 | U32 | 0.001 | A |
| freq | 18442 | 0x480A | U32 | 0.001 | Hz |
| v1n, v2n, v3n | 18444, 18446, 18448 | 0x480C-0x4810 | U32 | 0.01 | V |
| v12, v23, v31 | 18452, 18454, 18456 | 0x4814-0x4818 | U32 | 0.01 | V |
| i1, i2, i3, i_n | 18458-18464 | 0x481A-0x4820 | U32 | 0.001 | A |
| s_nom | 18474 | 0x482A | U32 | 1 | VA |
| p_total, q_total | 18476, 18478 | 0x482C, 0x482E | S32 | 1 | W / var |
| s_total | 18484 | 0x4834 | U32 | 1 | VA |
| pf_total | 18486 | 0x4836 | S16 | 0.001 | - |
| p1, p2, p3 | 18488-18492 | 0x4838-0x483C | S32 | 1 | W |
| q1, q2, q3 | 18494-18498 | 0x483E-0x4842 | S32 | 1 | var |
| s1, s2, s3 | 18512-18516 | 0x4850-0x4854 | U32 | 1 | VA |
| pf1, pf2, pf3 | 18518-18520 | 0x4856-0x4858 | S16 | 0.001 | - |

## Energy counters (total block 0x4D83+)

| Point | Dec address | Hex | Type | Scale | Unit |
|---|---|---|---|---|---|
| kwh_import (Total Ea+) | 19843 | 0x4D83 | U32 | 1 | kWh |
| kwh_export (Total Ea-) | 19846 | 0x4D86 | U32 | 1 | kWh |
| kvarh_import (Total Er+) | 19849 | 0x4D89 | U32 | 1 | kvarh (printed "varh 10e3") |
| kvah_total (Total Eap) | 19855 | 0x4D8F | U32 | 1 | kVAh |

## Not in this template (visible gaps, not plug-and-play)

- Residual sub-resolution counters (rEa+ 19845 Wh 10e-1 U16, rEa- 19848, rEr+ 19851 varh 10e-1,
  rEap 19857 VAh 10e-1): redundant companion words to the main counters, excluded to keep the
  point list readable.
- Per-tariff counters, per-load energy breakdowns, Er- and quadrant reactive counters, loads 2
  and 3 (blocks 0x4900+ / 0x4980+), the Network Inst block (36864 / 0x9000+), harmonic
  sub-blocks, avg/min-max blocks, DATETIME stamps, STRING_NORM product-name registers.
- PF type flags (18487, 18521-18523: 0 undefined / 1 leading / 2 lagging, U8): the register
  map has no enum type. Per-phase power-factor SIGN is readable from the scaled value; the
  leading/lagging flag is left to a later template revision.

## Not done on purpose

Settings blocks marked READ|WRITE in the source (load status enable 0x4800, tariffs, demand
configuration, PIN-protected writes) are WRITABLE meter configuration. The agent only reads;
writes are not implemented and would need the platform's approval path.
