# Acrel APM800 / APM801 / APM810 (three-phase network power meter)

Sources (Acrel's own domains, fetched 2026-10-20):
- "APM series Power Meters - Installation and Operation Instruction V2.4" (English),
  jsacrel.cn/uploads/soft/240410/2-240410162227.pdf (Jiangsu Acrel, the manufacturer).
- "APM 系列网络电力仪表" (Chinese original), pse.acrel.cn/uploads/soft/dngl/31.pdf
  (Acrel's main domain acrel.cn) - used to resolve the English manual's energy-address
  inconsistency and for the worked IEEE-754 example.
Rendered pages pixel-inspected against the extracted tables: EN p.35 (Modbus intro + comm
settings), EN p.38 (primary measurements map), EN p.45 (primary energy map), EN p.53 (8.3
worked examples), CN p.40 (8.3.4 with the IEEE-754 bit breakdown). Nothing here has been run
against a real meter.

## Link

Modbus RTU on RS-485 (COMM1); the extended second RS-485 and the Ethernet (MCE) option speak
Modbus-TCP with ONLY FC 0x03 supported there. The template reads with FC 0x03 (read holding
registers), which both paths support. Serial parameters are programmed on the meter:
baud 38400/19200/9600 (default)/4800/2400/1200; parity+stop combinations none+1 stop
(default), none+2 stop, odd+1, even+1. Match the Edge setup form to the meter's settings.

## Addressing and value format (all vendor-stated)

- The register table is 0-based: register 0 is the meter's Modbus address, and every worked
  example reads at the exact table numbers (e.g. voltage at 1120-1121, energy at 300-301),
  so table numbers are the wire (PDU) addresses.
- Int32: the manual states "for Int32 type data, the high bits are in the front and the low
  bits are in the back", and every Int32 example computes Val_t = first word x 65536 +
  second word (e.g. 9 x 65536 + 10176 = 600000 -> 60 kV). High word first, ABCD.
- Float: the Chinese original's 8.3.4 gives the full IEEE-754 bit breakdown for a readout of
  "2 word, arranged high to low, 4 byte (0x474B, 0xAC00)" = 52140 Wh - the first word holds
  the sign and exponent, so floats are also high-word-first (ABCD), standard IEEE-754.
- Signedness is real: Int16/Int32 are signed (power factor and power go negative), and the
  float format has an explicit sign bit, so signed W/var/PF are well-defined.

## Measurements (read-only, FC 03, primary-side values)

| Point | Registers | Type / scale | Unit |
|---|---|---|---|
| ia, ib, ic, i_n | 1100, 1102, 1104, 1106 | Int32 x0.001 | A |
| va_n, vb_n, vc_n | 1120, 1122, 1124 | Int32 x0.1 | V (L-N) |
| va_b, vb_c, vc_a | 1128, 1130, 1132 | Int32 x0.1 | V (L-L) |
| p_a, p_b, p_c, p_total | 1150, 1152, 1154, 1156 | Float x0.01 | W |
| q_a, q_b, q_c, q_total | 1158, 1160, 1162, 1164 | Float x0.01 | var |
| s_a, s_b, s_c, s_total | 1166, 1168, 1170, 1172 | Float x0.01 | VA |
| freq | 1179 | Int16 x0.01 | Hz |
| pf_a, pf_b, pf_c, pf_total | 1180, 1181, 1182, 1183 | Int16 x0.001 | (signed) |

Note the oddity the manual itself documents: the primary power floats carry the scaled value
(readout 110000.0 -> 1100 W at resolution 0.01), so the template applies scale 0.01 on f32.

## Energies (read-only, FC 03, Float, primary energy)

| Point | Registers | Unit |
|---|---|---|
| energy_imp | 3000-3001 | Wh |
| energy_exp | 3002-3003 | Wh |
| varh_inductive | 3004-3005 | varh |
| varh_capacitive | 3006-3007 | varh |

Doc inconsistency, resolved: the English 8.3.4 example reads "IMP" at 3050-3051 while its own
table puts IMP at 3000-3001. The Chinese original's table has BOTH rows: 3000-3001 is IMP and
3050-3051 is 正向总有功电能(T) (positive total active energy, tariff T, with T1-T4 breakdown at
3052-3059). The example simply used the tariff-total register under the IMP name. The template
uses the table rows (3000-3007); the tariff block is excluded (scope).

## Excluded

Secondary-side (CT-secondary) readings at 242-307, average currents/voltages, unbalance and
angle rows, demand and maximum/minimum blocks with timestamps, harmonics (4501+ and the
2nd-63rd tables), THD/TOHD/TEHD, tariff and monthly energy blocks (3050+), temperature and
I/O module rows, event/alarm records, and every RW settings register (writes) - the template
is read-only. APM830 uses a different layout (its 3050 block is monthly energy) and is NOT
covered by this template.
