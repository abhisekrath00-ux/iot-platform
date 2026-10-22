# CHINT DTSU666 / DSSU666 - Modbus register map provenance

Template `chint-dtsu666` in `web/src/lib/edgeSetup.ts`. Never run on a real meter; the edge
agent only reads.

## Source (vendor domain only)

- "DTSU666 DSSU666 User Manual" ZTW0.464.0104 V1 (the shipping user manual; Table 11
  "ModBus protocol address table" plus the communication-settings section), from
  https://www.chintglobal.com/content/dam/chint/global/product-center/instruments-meters/electricity-meter/din-rail-meter/dtsu666/manual/DTSU666%20DSSU666%20User%20Manual.pdf
- Cross-checked against CHINT Europe's "ModBus protocol address table - DTSU666" for the
  DTSU666_G variant (chint.eu): SAME quantities but a DIFFERENT map (instantaneous at
  0200H, energies at 0100H, plus combination energies and a second circuit at 0900H/0800H).
  This template follows the shipping user manual's map (instantaneous at 2000H, energies
  at 101EH). Do not mix the two maps; a DTSU666_G unit needs its own template.

No third-party copies were used.

## Conventions

1. Read function: FC 03. The manual states "ModBus_RTU protocol read command is 03H,
   write command is 10H" - vendor-stated.
2. Word order: abcd (MSW first). Table 11 Note 1: "Single-precision floating point adopts
   standard IEEE754 format, total 32 bit... ABCD (high type in the front, low byte
   behind)" - vendor-stated.
3. Register base: NOT STATED. The table lists parameter addresses in hex starting at
   0000H and never says whether the on-wire address is the listed address or the listed
   address minus 1. This template ships the listed addresses and carries the
   `pduBaseUnverified` gate: the installer picks the base explicitly in the UI before the
   config can be generated.
4. Scales: fixed multipliers from the table's unit column applied to the IEEE754 float:
   V x0.1, A x0.001, W x0.1, var x0.1, PF x0.001, Hz x0.01; energies are floats already
   in kWh (scale 1). Vendor-stated.
5. Factory serial settings: "the factory default communication parameter is ModBus-RTU
   protocol, the baud rate is 9600bps, with the calibration bit and stop bit to be n.1,
   and the instrument address to be 1" (also NOTE2: 9600bps, N.8.1) - vendor-stated;
   surfaced as a hint with an explicit Apply button, never silently applied.

## What is verified line-by-line against manual Table 11 (ZTW0.464.0104 V1)

| Point          | Listed | Description (manual)                          | Format | Scale | Unit |
|----------------|--------|-----------------------------------------------|--------|-------|------|
| kwh_import     | 101EH  | (current) Total Forward active energy         | float  | 1     | kWh  |
| kwh_import_a   | 1020H  | A Forward active energy                       | float  | 1     | kWh  |
| kwh_import_b   | 1022H  | B Forward active energy                       | float  | 1     | kWh  |
| kwh_import_c   | 1024H  | C Forward active energy                       | float  | 1     | kWh  |
| kwh_import_net | 1026H  | Net Forward active energy                     | float  | 1     | kWh  |
| kwh_export     | 1028H  | (current) Total Reverse active energy         | float  | 1     | kWh  |
| kwh_export_a   | 102AH  | A Reverse active energy                       | float  | 1     | kWh  |
| kwh_export_b   | 102CH  | B Reverse active energy                       | float  | 1     | kWh  |
| kwh_export_c   | 102EH  | C Reverse active energy                       | float  | 1     | kWh  |
| kwh_export_net | 1030H  | Net Reverse active energy                     | float  | 1     | kWh  |
| v12            | 2000H  | Uab line voltage                              | float  | 0.1   | V    |
| v23            | 2002H  | Ubc line voltage                              | float  | 0.1   | V    |
| v31            | 2004H  | Uca line voltage                              | float  | 0.1   | V    |
| v1             | 2006H  | Ua phase voltage (invalid for 3-wire)         | float  | 0.1   | V    |
| v2             | 2008H  | Ub phase voltage (invalid for 3-wire)         | float  | 0.1   | V    |
| v3             | 200AH  | Uc phase voltage (invalid for 3-wire)         | float  | 0.1   | V    |
| i1             | 200CH  | Ia phase current                              | float  | 0.001 | A    |
| i2             | 200EH  | Ib phase current                              | float  | 0.001 | A    |
| i3             | 2010H  | Ic phase current                              | float  | 0.001 | A    |
| p_total        | 2012H  | Pt combined active power                      | float  | 0.1   | W    |
| p1             | 2014H  | Pa phase A active power                       | float  | 0.1   | W    |
| p2             | 2016H  | Pb phase B active power (invalid for 3-wire)  | float  | 0.1   | W    |
| p3             | 2018H  | Pc phase C active power                       | float  | 0.1   | W    |
| q_total        | 201AH  | Qt combined reactive power                    | float  | 0.1   | var  |
| q1             | 201CH  | Qa phase A reactive power                     | float  | 0.1   | var  |
| q2             | 201EH  | Qb phase B reactive power (invalid for 3-wire)| float  | 0.1   | var  |
| q3             | 2020H  | Qc phase C reactive power                     | float  | 0.1   | var  |
| pf_total       | 202AH  | PFt combined power factor (x0.001)            | float  | 0.001 | -    |
| pf1            | 202CH  | PFa phase A power factor (invalid for 3-wire) | float  | 0.001 | -    |
| pf2            | 202EH  | PFb phase B power factor (invalid for 3-wire) | float  | 0.001 | -    |
| pf3            | 2030H  | PFc phase C power factor (invalid for 3-wire) | float  | 0.001 | -    |
| freq           | 2044H  | Frequency                                     | float  | 0.01  | Hz   |

32 points. Vendor-stated sign convention: PF positive = inductive, negative = capacitive.
Vendor-stated wiring caveat: phase voltages and per-phase power/PF rows are invalid on
three-phase three-wire hookups.

## Excluded, and why

- Apparent power (VA) and apparent energy (kVAh): NOT in the vendor map at all. Do not
  synthesize them from other points in a template.
- Reactive energy registers (kvarh): the manual's Table 11 omits them (the DTSU666_G
  variant table has EqCom1/EqCom2 at 010AH/0114H on ITS map; not valid on this map).
- Combination energies EpCom (0100H exists only on the _G map): not on this map.
- All keyboard/setup registers (0000H-0045H: software version, CT/VT ratios, address,
  baud, protocol switch, energy reset CLr.E): configuration, not monitoring; some are
  writable and CLr.E clears energy - never touched.
- Primary-side data and "other addresses": the manual's Note 2 says they need the
  detailed communication protocol, which is not public on the vendor site.
- Demand, max/min, THD, harmonics, tariffs, clock: not in the vendor map.
