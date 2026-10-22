# CET PMC-53M (PMC-53 series: PMC-53V / PMC-53I / PMC-53M) - Modbus register map provenance

Template `cet-pmc53m` in `web/src/lib/edgeSetup.ts`. Never run on a real meter; the edge
agent only reads.

## Source (vendor domain only)

- "PMC-53 Series User Manual V5.2A" (56 pages), Chapter 5 "Modbus Register Map" (Protocol
  Version 6.0), from
  https://www.cet-global.com/uploadfiles/2021/07/PMC-53%20Series%20User%20Manual_V5.2A.pdf

No third-party copies were used. (At capture time cet-global.com served a broken TLS chain
to the build sandbox; the PDF bytes are the Wayback Machine capture of the same vendor
URL.) The series has exactly three models - PMC-53V, PMC-53I, PMC-53M; this template
targets the PMC-53M and every included point is marked for model M in the manual's model
column.

## Conventions

1. Read function: FC 03 holding registers. Vendor-stated in the Chapter 5 preamble ("Read
   Holding Registers (Function Code 0x03)"); DO control uses FC 05 coils and is excluded.
2. Register base: the manual states the addresses are "listed without the Modbus Address
   Prefix, 4" and that "it's not necessary to subtract 400001" - the listed number is the
   on-wire (PDU) address verbatim (40000 -> 0x9C40).
3. Scale convention: Table 5-1 note 2 defines it with a worked example - "x100, V" means
   the register holds 100x the engineering value (raw 22003 = 220.03 V). The template's
   scales are the reciprocals (0.01, 0.001, 0.1).
4. WORD ORDER IS NOT STATED for the 5.1/5.2 multi-register values. The manual's demand-log
   structure (Table 5-5) lists "High-order Word" at offset +0 and "Low-order Word" at +1,
   which is why `abcd` is preselected - but that table is not the measurement map, so the
   template sets `wordOrderUnverified: true`: the installer picks the word order from a
   known reading (e.g. frequency or one voltage) against the meter's display before
   trusting any value.
5. Factory serial settings (setup register table 41013-41015, defaults marked with * or
   printed): Unit ID default 100, baud default 9600 (3=9600*), comm config default 8E1
   (2=8E1*, i.e. even parity, 1 stop bit). Surfaced as a hint with an explicit Apply
   button; the installer's edited values are never silently replaced.
6. Values are engineering values with the meter's PT/CT ratios applied (setup registers
   41010/41011, defaults 1); the manual does not separately label primary vs secondary, so
   the provisional text carries the factory-1:1 caution.

## What is verified line-by-line against Table 5-1 / Table 5-3 (PMC-53M points)

| Point         | Register | Description (manual) | Format | Scale | Unit  |
|---------------|----------|----------------------|--------|-------|-------|
| v1            | 40000    | Va                   | UINT32 | 0.01  | V     |
| v2            | 40002    | Vb                   | UINT32 | 0.01  | V     |
| v3            | 40004    | Vc                   | UINT32 | 0.01  | V     |
| v_avg_ln      | 40006    | VLN average          | UINT32 | 0.01  | V     |
| v12           | 40008    | Vab                  | UINT32 | 0.01  | V     |
| v23           | 40010    | Vbc                  | UINT32 | 0.01  | V     |
| v31           | 40012    | Vca                  | UINT32 | 0.01  | V     |
| v_avg_ll      | 40014    | VLL average          | UINT32 | 0.01  | V     |
| i1            | 40016    | Ia                   | UINT32 | 0.001 | A     |
| i2            | 40018    | Ib                   | UINT32 | 0.001 | A     |
| i3            | 40020    | Ic                   | UINT32 | 0.001 | A     |
| i_avg         | 40022    | I average            | UINT32 | 0.001 | A     |
| p1            | 40024    | kWa                  | INT32  | 0.001 | kW    |
| p2            | 40026    | kWb                  | INT32  | 0.001 | kW    |
| p3            | 40028    | kWc                  | INT32  | 0.001 | kW    |
| p_total       | 40030    | Sum kW               | INT32  | 0.001 | kW    |
| q1            | 40032    | kvara                | INT32  | 0.001 | kvar  |
| q2            | 40034    | kvarb                | INT32  | 0.001 | kvar  |
| q3            | 40036    | kvarc                | INT32  | 0.001 | kvar  |
| q_total       | 40038    | Sum kvar             | INT32  | 0.001 | kvar  |
| s1            | 40040    | kVAa                 | INT32  | 0.001 | kVA   |
| s2            | 40042    | kVAb                 | INT32  | 0.001 | kVA   |
| s3            | 40044    | kVAc                 | INT32  | 0.001 | kVA   |
| s_total       | 40046    | Sum kVA              | INT32  | 0.001 | kVA   |
| pf1           | 40048    | PFa                  | INT16  | 0.001 | -     |
| pf2           | 40049    | PFb                  | INT16  | 0.001 | -     |
| pf3           | 40050    | PFc                  | INT16  | 0.001 | -     |
| pf_total      | 40051    | Sum PF               | INT16  | 0.001 | -     |
| freq          | 40052    | FREQ                 | UINT16 | 0.01  | Hz    |
| i_neutral     | 40053    | I4                   | UINT32 | 0.001 | A     |
| kwh_import    | 40100    | kWh Import           | UINT32 | 0.1   | kWh   |
| kwh_export    | 40102    | kWh Export           | UINT32 | 0.1   | kWh   |
| kvarh_import  | 40106    | kvarh Import         | UINT32 | 0.1   | kvarh |
| kvarh_export  | 40108    | kvarh Export         | UINT32 | 0.1   | kvarh |

Energy note (Table 5-3): counters max out at 999,999,999 in units of 0.1 kXh (i.e.
99,999,999.9 kXh) and roll over to zero automatically - vendor-stated.

## Excluded, and why

- Demand and peak-demand registers (40060-40070, 40500+): demand logs with timestamps
  (Table 5-5), not instantaneous points.
- Voltage/current angle registers (40072-40077), voltage/current unbalance (40055/40056),
  displacement PF (40057-40059): low-value for charting, several need newer firmware.
- I residual (40078) and Temperature (40079): need the optional external sensors and use
  sentinel values (0x7FFF / 30000) when absent; skipped to avoid fake readings.
- Alarm/DO/DI status bitmaps and SOE pointer (40095-40098): bit-packed state, not charted
  measurements.
- Harmonic measurements (40703+): per-order tables, out of scope.
- All setup registers (41000+) and the Clear Energy write (41019): configuration surface,
  never polled by a read template.

## Firmware and wiring cautions (carried in the provisional text)

- I4 (neutral current) and the unbalance/dPF rows need firmware V1.20.01 and Protocol
  Version 6.0 or later (Table 5-1 * marker).
- In Delta wiring mode the L-N voltages, per-phase kW/kvar/kVA and PF registers have no
  meaning and are reserved (Table 5-1 note 1).
