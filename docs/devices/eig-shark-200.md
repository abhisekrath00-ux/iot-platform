# Electro Industries/GaugeTech Shark 200 - Modbus register map provenance

Template `eig-shark-200` in `web/src/lib/edgeSetup.ts`. Never run on a real meter; the edge
agent only reads.

## Source (vendor domain only)

- "Shark 200 User Manual_Modbus Map_pagesfromusermanual.pdf" - Appendix B "Modbus Map and
  Retrieving Logs" of the Shark 200 Meter User Manual, Doc# E149701 (81 pages, register
  map MM-1 to MM-40), from
  https://www.electroind.com/products/Shark200/pdf/manuals/Shark%20200%20User%20Manual_Modbus_Map_pagesfromusermanual.pdf
- "Shark 200 Meter Quickstart" E149703 V.1.12 (factory serial defaults, page QS-5), from
  https://www.electroind.com/products/Shark200/pdf/quickstarts/Shark-200-Meter-Quickstart_E149703.pdf

No third-party copies were used. The register table below was read from the rendered
vendor pages (MM-2, MM-3, MM-7, MM-8), not from text extraction alone.

## Conventions - all vendor-stated

1. Read function: FC 03 holding registers. Appendix B's notes state "All references assume
   the use of Modbus function codes 0x03, 0x06, and 0x10" (0x06/0x10 are writes; the map
   is read with 0x03).
2. Register base: listed minus 1. Section B.6 works the SCADA addressing through
   explicitly: "take 40000 and add the value of the register (Address) in the decimal
   column" (e.g. register 9 -> 40009), and the drivers "strip off the leading four and
   subtract 1 from the remaining value" to build the actual Modbus message. Template
   registers are stored as on-wire (listed decimal - 1).
3. Word order: MSW first (abcd). Section B.3 Data Formats: for SINT32/UINT32 "the
   lower-addressed register is the high order half" and for FLOAT (32-bit IEEE 754) "the
   lower-addressed register is the high order half (i.e., contains the exponent)".
4. Formats: FLOAT (IEEE 754, 2 registers) for the Primary Readings Block, with fixed real
   units (volts, amps, watts, VARs, VAs, Hz, unitless PF -1..+1). THD block and current
   unbalance are UINT16 with a printed fixed resolution of 0.01 (%).
5. Factory serial settings (quickstart page QS-5, "Factory Initial Default Settings"):
   Device Address 1, Baud Rate 57600, Protocol Modbus RTU. Surfaced as a hint with an
   explicit Apply button; the installer's edited values are never silently replaced.
   Parity/data bits are not stated in the quickstart - match the meter.

## What is verified line-by-line against map pages MM-2, MM-3, MM-7 and MM-8

| Point        | Listed | PDU  | Description (map)          | Format | Scale | Unit |
|--------------|--------|------|----------------------------|--------|-------|------|
| v1           | 1000   | 999  | Volts A-N                  | FLOAT  | 1     | V    |
| v2           | 1002   | 1001 | Volts B-N                  | FLOAT  | 1     | V    |
| v3           | 1004   | 1003 | Volts C-N                  | FLOAT  | 1     | V    |
| v12          | 1006   | 1005 | Volts A-B                  | FLOAT  | 1     | V    |
| v23          | 1008   | 1007 | Volts B-C                  | FLOAT  | 1     | V    |
| v31          | 1010   | 1009 | Volts C-A                  | FLOAT  | 1     | V    |
| i1           | 1012   | 1011 | Amps A                     | FLOAT  | 1     | A    |
| i2           | 1014   | 1013 | Amps B                     | FLOAT  | 1     | A    |
| i3           | 1016   | 1015 | Amps C                     | FLOAT  | 1     | A    |
| p_total      | 1018   | 1017 | Watts, 3-Ph total          | FLOAT  | 1     | W    |
| q_total      | 1020   | 1019 | VARs, 3-Ph total           | FLOAT  | 1     | var  |
| s_total      | 1022   | 1021 | VAs, 3-Ph total            | FLOAT  | 1     | VA   |
| pf_total     | 1024   | 1023 | Power Factor, 3-Ph total   | FLOAT  | 1     | -    |
| freq         | 1026   | 1025 | Frequency (0 to 65.00)     | FLOAT  | 1     | Hz   |
| i_neutral    | 1028   | 1027 | Neutral Current            | FLOAT  | 1     | A    |
| p1           | 1030   | 1029 | Watts, Phase A             | FLOAT  | 1     | W    |
| p2           | 1032   | 1031 | Watts, Phase B             | FLOAT  | 1     | W    |
| p3           | 1034   | 1033 | Watts, Phase C             | FLOAT  | 1     | W    |
| q1           | 1036   | 1035 | VARs, Phase A              | FLOAT  | 1     | var  |
| q2           | 1038   | 1037 | VARs, Phase B              | FLOAT  | 1     | var  |
| q3           | 1040   | 1039 | VARs, Phase C              | FLOAT  | 1     | var  |
| s1           | 1042   | 1041 | VAs, Phase A               | FLOAT  | 1     | VA   |
| s2           | 1044   | 1043 | VAs, Phase B               | FLOAT  | 1     | VA   |
| s3           | 1046   | 1045 | VAs, Phase C               | FLOAT  | 1     | VA   |
| pf1          | 1048   | 1047 | Power Factor, Phase A      | FLOAT  | 1     | -    |
| pf2          | 1050   | 1049 | Power Factor, Phase B      | FLOAT  | 1     | -    |
| pf3          | 1052   | 1051 | Power Factor, Phase C      | FLOAT  | 1     | -    |
| i_unbalance  | 1065   | 1064 | Current Unbalance          | UINT16 | 0.01  | %    |
| thd_v1       | 6000   | 5999 | Volts A-N, %THD            | UINT16 | 0.01  | %    |
| thd_v2       | 6001   | 6000 | Volts B-N, %THD            | UINT16 | 0.01  | %    |
| thd_v3       | 6002   | 6001 | Volts C-N, %THD            | UINT16 | 0.01  | %    |
| thd_i1       | 6003   | 6002 | Amps A, %THD               | UINT16 | 0.01  | %    |
| thd_i2       | 6004   | 6003 | Amps B, %THD               | UINT16 | 0.01  | %    |
| thd_i3       | 6005   | 6004 | Amps C, %THD               | UINT16 | 0.01  | %    |

34 points. Vendor-stated behavior: per-phase power and PF have values only for WYE
hookups and read zero for all other hookups (map comment on MM-3).

## Excluded, and why

- ALL energy counters (Primary Energy Block 1500+, interval energies, uncompensated
  energies): SINT32 "per energy format" - the map states the decimal point is implied
  "per energy format" AND the resolution of the last digit is units/kilo/mega "per energy
  format". Both the scale and the unit multiplier follow the meter's programmable
  energy-format setting, so no honest fixed scale can ship in a static template. Read
  energies via the meter display or Communicator EXT, or add them after confirming the
  meter's energy-format setting on site.
- Integer Readings Block 279-305: same class of problem - the map comment says to "use
  the settings from Programmable settings for scale and decimal point location". The
  FLOAT Primary Readings Block carries the same quantities with fixed units, so nothing
  is lost.
- Secondary Readings Section 40001+: scaled-integer secondaries (depend on CT/VT
  programming); the primary FLOAT block is the right source.
- Demand blocks (1997+), phase angles (4100+), min/max blocks, symmetrical components
  beyond current unbalance, per-harmonic magnitudes/phases (6206+), wave scope data,
  operating-mode readings, option-card blocks, accumulators, logs and every
  programming/setup register: out of the monitoring template's scope or write-capable.
