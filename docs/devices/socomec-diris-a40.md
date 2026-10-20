# Socomec DIRIS A-40, load 1 (three-phase panel meter)

Source: the manufacturer's "MODBUS tables of DIRIS A-40" communication table, document
CMT-1.7.4B EN (emea.socomec.com, fetched 2026-10-20). Nothing here has been run against a real
meter. The A-40 is a panel meter with three load channels; this profile reads the LOAD 1
instantaneous block plus nothing else.

## Link

RS-485 / Modbus RTU or Modbus TCP per the A-40 variant ordered. Address, baud and parity are
configured on the meter's display. Values are SIGNED and UNSIGNED INTEGERS with documented
scaling, NOT floats: voltages in V 10e-2 (scale 0.01), currents in mA (0.001), frequency in mHz
(0.001), power factors in 1/1000 (0.001, negative = reverse). 32-bit values are two registers,
most significant register first (`abcd`).

## Measurements (read-only, input registers FC 04, load 1 block 0x4800+)

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

PF type flags (18487, 18521-18523: 0 undefined / 1 leading / 2 lagging, U8) are NOT in the
profile: the register map has no enum type. Per-phase power-factor SIGN is readable from the
scaled value; the leading/lagging flag is left to a later template revision.

## Not in this template yet (visible gaps, not plug-and-play)

- ENERGY COUNTERS (kWh import/export per load and per tariff): the fetched communication-table
  page was truncated before the energy sections, so no energy registers are claimed here. Fetch
  the complete table (or the meter's own display map) before adding any.
- Loads 2 and 3 (blocks 0x4900+ / 0x4980+), harmonic sub-blocks, avg/min-max blocks, DATETIME
  stamps, STRING_NORM product-name registers.

## Not done on purpose

Settings blocks marked READ|WRITE in the source (load status enable 0x4800, tariffs, demand
configuration) are WRITABLE meter configuration. The agent only reads; writes are not
implemented and would need the platform's approval path.
