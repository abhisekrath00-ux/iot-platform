# Circutor CVM-C10 (three-phase power analyzer)

Source (Circutor's official docs domain, fetched 2026-10-20):
- "CVM-C10 Instruction Manual" M001B01-03-23A, docs.circutor.com/docs/M001B01-03.pdf,
  section 4.10 MODBUS (protocol, frame examples, memory map tables 19/20) and section
  4.9.29 (serial programming screens).
Rendered pages pixel-inspected against the extracted tables before this map was written:
the FC 04 reading example (manual page 73), Table 19 measurement variables (page 74),
Table 19 continuation + Table 20 energy variables (page 75), Table 20 continuation (page 76).
Nothing here has been run against a real meter.

## Link

Modbus RTU on the RS-485 port (the device also speaks BACnet; this template is Modbus only).
The manual states FC 0x03 AND 0x04 are implemented for the measurement and energy variables;
this template uses FC 04. A frame is limited to 20 variables (40 registers) per the manual's
note. Serial parameters are PROGRAMMED ON THE DEVICE (section 4.9.29): 9600 or 19200 baud,
parity none/even/odd, 7 or 8 data bits, 1 or 2 stop bits, peripheral address 0-255. Match the
Edge setup form to what the device is configured with, and keep the device at 8 data bits -
the agent's serial config is 8 data bits.

## Addressing and value format (verified by the manual's own frame example)

Section 4.10.2.1 reads "the instantaneous value of the phase voltage of L1" with FC 04,
initial register 0000, count 0002, and the response carries registers 0000 084D explained as
VL1 x10 = 212.5 V. This proves, from the vendor's own document: (a) the listed table address
goes on the wire unmodified (V1 is listed at 00-01 and the example starts at 0000), (b) the
high word of a 32-bit value comes first (big-endian, ABCD), and (c) the voltage scale is
V x10. All addresses in the tables are hexadecimal.

## The excluded signed quantities

The device shows generated power with a negative sign on its display, but the manual nowhere
states how negative values are encoded on the wire (two's complement, sign-magnitude, or
separate counters). Rather than guess, this template EXCLUDES every signed-capable quantity:
active power (kW rows), inductive/capacitive reactive power (kvarL/kvarC rows), power factor,
and cos phi. What remains is unsigned by nature: voltages, currents, THD, frequency,
apparent power (kVA), and the separate consumed/generated energy counters. To add power and
PF later, confirm the negative encoding against a real unit or a Circutor document that
states it for this model.

## Measurements (read-only, FC 04, hex addresses from Table 19)

| Point | Address | Scale | Unit |
|---|---|---|---|
| va_n, vb_n, vc_n | 0x00, 0x10, 0x20 | V x10 | V (phase, L-N) |
| ia, ib, ic | 0x02, 0x12, 0x22 | mA | A |
| sa, sb, sc | 0x0A, 0x1A, 0x2A | 1 | VA |
| s_total | 0x36 | 1 | VA (apparent three-phase, "kVA III") |
| freq | 0x3C | Hz x100 | Hz |
| va_b, vb_c, vc_a | 0x3E, 0x40, 0x42 | V x10 | V (L-L) |
| i_n | 0x44 | mA | A (measured neutral current) |
| thd_v_a, thd_v_b, thd_v_c | 0x46, 0x48, 0x4A | % x10 | % (voltage THD) |
| thd_i_a, thd_i_b, thd_i_c | 0x4C, 0x4E, 0x50 | % x10 | % (current THD) |

## Energies (read-only, FC 04, hex addresses from Table 20, "Total" column, base units)

| Point | Address | Unit |
|---|---|---|
| energy_consumed | 0xDE | Wh |
| energy_generated | 0xF2 | Wh |
| reactive_ind_consumed | 0xE2 | varh |
| reactive_cap_consumed | 0xE6 | varh |
| reactive_ind_generated | 0xF6 | varh |
| reactive_cap_generated | 0xFA | varh |
| apparent_consumed | 0xEA | VAh |
| apparent_generated | 0xFE | VAh |

## Excluded

All kW/kvarL/kvarC/PF/CosPhi rows (signed encoding unstated, see above), maximum/minimum
columns, maximum-demand rows (0x52-0x5D, 0x200-0x207), per-tariff energy columns (T1-T3),
CO2/cost/hours rows, the harmonics table (Table 21), and all write functions (relay writes,
max/min deletion, configuration) - the template is read-only.
