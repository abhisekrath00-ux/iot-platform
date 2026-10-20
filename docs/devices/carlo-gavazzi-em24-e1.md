# Carlo Gavazzi EM24-DIN E1 (Ethernet) - Modbus register map provenance

Template `carlo-gavazzi-em24-e1` in `web/src/lib/edgeSetup.ts`. Never run on a real
meter; the edge agent only reads.

## Source (vendor domain only)

- Document: "EM24_E1_CP" (EM24 Ethernet Communication Protocol), hosted by Carlo
  Gavazzi Automation:
  https://www.gavazziautomation.com/fileadmin/images/PIM/OTHERSTUFF/COMPRO/EM24_E1_CP.pdf
- Scope: EM24-DIN E1, the RJ45 / Modbus TCP variant ("The RJ45 interface supports the
  Modbus TCP/IP protocol"). The serial EM24 variants are a different document and are
  NOT covered by this template.

## Conventions - all vendor-stated, no installer gate needed

1. Read function: section 1.2 states function codes 03h and 04h "have exactly the same
   effect and can be used indifferently" for reading. Template fixes func 4; no gate.
   (FC 06 write exists but is never used - the platform only reads.)
2. Register base: the table prints BOTH the 6-digit Modicom address AND the "Physical
   address", defined as "the word address value to be included in the communication
   frame". Template uses the physical addresses verbatim; no base gate.
3. Word order: section 2.1 states "the byte order (inside the single word) is MSB->LSB.
   In INT32, UINT32 and UINT64 formats, the word order is LSW->MSW" - i.e. cdab for the
   32-bit integers this map uses. Vendor-stated; no gate. (The IEEE754 SP float format
   is listed but NOT given a word-order statement - this template uses no float points,
   so nothing is assumed.)

## What is verified line-by-line against section 2.3.1

All values are signed INT32 in cdab word order unless noted; scale = 1/weight:

| Point        | Modicom | Physical | Variable      | Weight     | Type   | Unit  |
|--------------|---------|----------|---------------|------------|--------|-------|
| v1n          | 300001  | 0000h    | V L1-N        | Volt*10    | INT32  | V     |
| v2n          | 300003  | 0002h    | V L2-N        | Volt*10    | INT32  | V     |
| v3n          | 300005  | 0004h    | V L3-N        | Volt*10    | INT32  | V     |
| v12          | 300007  | 0006h    | V L1-L2       | Volt*10    | INT32  | V     |
| v23          | 300009  | 0008h    | V L2-L3       | Volt*10    | INT32  | V     |
| v31          | 300011  | 000Ah    | V L3-L1       | Volt*10    | INT32  | V     |
| i1           | 300013  | 000Ch    | A L1          | Ampere*1000| INT32  | A     |
| i2           | 300015  | 000Eh    | A L2          | Ampere*1000| INT32  | A     |
| i3           | 300017  | 0010h    | A L3          | Ampere*1000| INT32  | A     |
| p1           | 300019  | 0012h    | W L1          | Watt*10    | INT32  | W     |
| p2           | 300021  | 0014h    | W L2          | Watt*10    | INT32  | W     |
| p3           | 300023  | 0016h    | W L3          | Watt*10    | INT32  | W     |
| s1           | 300025  | 0018h    | VA L1         | VA*10      | INT32  | VA    |
| s2           | 300027  | 001Ah    | VA L2         | VA*10      | INT32  | VA    |
| s3           | 300029  | 001Ch    | VA L3         | VA*10      | INT32  | VA    |
| q1           | 300031  | 001Eh    | VAR L1        | var*10     | INT32  | var   |
| q2           | 300033  | 0020h    | VAR L2        | var*10     | INT32  | var   |
| q3           | 300035  | 0022h    | VAR L3        | var*10     | INT32  | var   |
| v_sum_ln     | 300037  | 0024h    | V L-N sum     | Volt*10    | INT32  | V     |
| v_sum_ll     | 300039  | 0026h    | V L-L sum     | Volt*10    | INT32  | V     |
| p_total      | 300041  | 0028h    | W sum         | Watt*10    | INT32  | W     |
| s_total      | 300043  | 002Ah    | VA sum        | VA*10      | INT32  | VA    |
| q_total      | 300045  | 002Ch    | VAR sum       | var*10     | INT32  | var   |
| pf1          | 300047  | 002Eh    | PF L1         | PF*1000    | INT16  | -     |
| pf2          | 300048  | 002Fh    | PF L2         | PF*1000    | INT16  | -     |
| pf3          | 300049  | 0030h    | PF L3         | PF*1000    | INT16  | -     |
| pf_total     | 300050  | 0031h    | PF sum        | PF*1000    | INT16  | -     |
| freq         | 300052  | 0033h    | Hz            | Hz*10      | UINT16 | Hz    |
| kwh_import   | 300053  | 0034h    | kWh(+) TOT    | kWh*10     | INT32  | kWh   |
| kvarh_import | 300055  | 0036h    | kvarh(+) TOT  | kvarh*10   | INT32  | kvarh |
| kwh_export   | 300079  | 004Eh    | kWh(-) TOT    | kWh*10     | INT32  | kWh   |
| kvarh_export | 300081  | 0050h    | kvarh(-) TOT  | kvarh*10   | INT32  | kvarh |

PF sign (vendor note on 300047): "Negative values correspond to lead(C), positive
value correspond to lag(L)."
Caveat (vendor): 300037/300039 are the SUM of the phase
voltages per the table's sigma notation - interpreted here as the document's own group
label suggests; validate one reading against the meter display before trending them.

## Exclusions

- Phase sequence (300051), DMD W / DMD W max (300057/300059), per-phase and per-tariff
  partial energies (300061-300077, 300111+), hour counter (300091): not needed for the
  base map.
- All 4xxxx holding registers are configuration (R/W); excluded. The platform never
  writes to the meter.

## Hardware status

Addresses, FC, types, word order and scaling are vendor-documented as above, but the
map has not been run against a physical EM24-DIN E1. Confirm real readings against the
meter display during the pilot; until then every value from this template is
hardware-untested.
