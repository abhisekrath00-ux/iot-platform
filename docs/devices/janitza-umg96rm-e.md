# Janitza UMG 96RM-E (three-phase power analyser)

Sources (both Janitza domains, fetched 2026-10-20):
- "UMG 96RM-E Modbus address list and formulary" (from firmware version 3.01 / hardware
  release 104), janitza.com/files/download/manuals/current/UMG96RM/E/janitza-mal-umg96rme-en.pdf
  - the "Frequently required readings" table (addresses 19000-19120) and the General chapter
  (Modbus functions, transfer parameters, byte sequence, number formats).
- "UMG 96RM-E operating manual" (janitza-bhb-umg96rme-en.pdf), Table 2 "Modbus address list
  (frequently needed measured values)".
Rendered pages of both tables (address list page 19, byte-sequence page 5) were inspected as
pixels against the extracted text before this map was written. Nothing here has been run
against a real meter.

## Link

Modbus TCP on the base unit's Ethernet port, or Modbus RTU on the RS485 interface with the
same addresses. As a slave the device supports FC 03 (Read Holding Registers) and FC 04
(Read Input Registers) - both stated in the "Modbus functions (slave)" section; this
template uses FC 04. Transfer parameters per the doc: 9600-115200 baud, 8 data bits, no
parity. Register contents update every 200 ms.

## Addressing (verified)

The numbers in the address list ARE the Modbus addresses used on the wire. Evidence: the
operating manual's Table 2 prints the same readings in a column literally headed "Modbus
address" (Voltage L1-N at 19000, etc.) alongside a separate "via Display" column for
on-device access, and the address list's byte-sequence note tells the reader to "add the
value 32768 to the address" for little-endian - an instruction that only makes sense if the
listed number is the address you put in the request.

## Value formats (all vendor-stated - no unverified convention)

Every point in this template is a 32-bit IEEE-754 FLOAT spanning 2 registers. Byte order is
documented in the "Byte sequence" section: the listed addresses supply data big-endian
(high byte before low byte, i.e. ABCD); adding 32768 to an address returns the same value
little-endian. Measured values in float format already include the configured transformer
factors (the doc's "Measured values" note); the SHORT-format variants of the same readings
do NOT and are excluded for that reason.

## Measurements (read-only, FC 04, addresses from the "Frequently required readings" table)

| Point | Address | Type | Unit |
|---|---|---|---|
| va_n, vb_n, vc_n | 19000, 19002, 19004 | Float | V (L-N) |
| va_b, vb_c, vc_a | 19006, 19008, 19010 | Float | V (L-L) |
| ia, ib, ic | 19012, 19014, 19016 | Float | A |
| i_n_vec | 19018 | Float | A ("Vector sum; IN=I1+I2+I3" - a vector sum, not a measured neutral current) |
| pa, pb, pc | 19020, 19022, 19024 | Float | W (per phase, L-N) |
| p_sum | 19026 | Float | W ("Sum; Psum3=P1+P2+P3") |
| sa, sb, sc | 19028, 19030, 19032 | Float | VA |
| s_sum | 19034 | Float | VA ("Sum; Ssum3=S1+S2+S3") |
| qa, qb, qc | 19036, 19038, 19040 | Float | var (fundamental reactive power) |
| q_sum | 19042 | Float | var ("Sum; Qsum3=Q1+Q2+Q3") |
| cosphi_a, cosphi_b, cosphi_c | 19044, 19046, 19048 | Float | - (CosPhi, fundamental component) |
| freq | 19050 | Float | Hz ("Measured frequency"; device frequency range 45..65 per the parameter list) |
| rotation_field | 19052 | Float | - (1=right, 0=none, -1=left) |
| energy_total | 19060 | Float | Wh ("Real energy L1..L3") |
| energy_consumed | 19068 | Float | Wh ("Real energy L1..L3, consumed") |
| energy_delivered | 19076 | Float | Wh ("Real energy L1..L3, delivered") |
| energy_apparent | 19084 | Float | VAh ("Apparent energy L1..L3") |
| energy_reactive_ind | 19100 | Float | varh ("Reactive energy ind. L1..L3") |
| energy_reactive_cap | 19108 | Float | varh ("Reactive energy cap. L1..L3") |
| thd_v_a, thd_v_b, thd_v_c | 19110, 19112, 19114 | Float | % (THD U, L-N) |
| thd_i_a, thd_i_b, thd_i_c | 19116, 19118, 19120 | Float | % (THD I) |

## Excluded

Per-phase energy rows (19054-19058, 19062-19066, 19070-19074, 19078-19082, 19086-19098,
19102-19106), mean/min/max tables, the fourier-analysis tables, the tariff-based energy
tables at 5000+, all RD/WR parameter addresses (device configuration stays out of a
read-only template), and the L4/current-input rows at 10085+ (option-dependent).
