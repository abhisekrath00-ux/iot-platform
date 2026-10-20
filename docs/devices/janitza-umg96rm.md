# Janitza UMG 96RM (power analyser, Modbus RTU)

Evidence (vendor domain only):
- Document: "Power Analyser UMG 96RM - Modbus-address list and Formulary (from firmware
  rel. 1.14)", janitza-mal-umg96rm-en.pdf, served from Janitza's own site:
  https://www.janitza.com/files/download/manuals/current/UMG96RM/Basic/janitza-mal-umg96rm-en.pdf
  Covers the UMG 96RM basic device plus extensions UMG 96RM-PN, -P and -CBM. Every row used
  below was verified by direct pixel inspection of the rendered vendor PDF pages (pages 5,
  17, 18) - not OCR, not third-party mirrors.
- User manual: "Power Analyser UMG 96 RM - Basic device, User manual and technical data",
  janitza-bhb-umg96rm-en.pdf, also from janitza.com:
  https://www.janitza.com/files/download/manuals/current/UMG96RM/Basic/janitza-bhb-umg96rm-en.pdf
  Its Table 2 (manual pages 94-96, pixel-verified) prints the same readings with an
  explicit "Modbus Address" column (19000-19120) next to the "Address Above display"
  column - this is the vendor's register-base statement: the listed Modbus addresses are
  the addresses on the wire. It also notes "The addresses above 1000 can only be
  processed via modbus!", confirming 19000+ is a Modbus-only range.

## Vendor-stated conventions

- Byte order: "The addresses described in this address list supply the data in the
  Big-Endian format." (high byte before low byte = ABCD). Adding 32768 to an address
  returns the same value in Little-Endian - this template does NOT use +32768.
- Serial (manual page 5): baud 9600 / 19200 / 38400 / 57600 / 115200, 8 data bits,
  parity none, stop bits 2 (UMG 96RM; 1 or 2 for external devices). The meter's own
  parameters are set in its menu; match the edge setup form to them.
- Update rate: registers refresh every 200 ms.
- Number formats: float = 32-bit IEEE 754; int/uint = 32-bit; long64 = 64-bit.

## Vendor-stated (continued)

- Register base: the user manual's Table 2 labels the 19000-19120 numbers "Modbus
  Address" (distinct from the "Address Above display" column), so listed numbers go on
  the wire unmodified. No base choice is needed.

## NOT vendor-stated (one installer gate, required before YAML)

- Read function code: the address list documents FC 03 (holding) and FC 04 (input) as
  both supported by the slave, but never binds the address list to one of them. Choose
  from your meter's documentation or by testing. (The sibling template for the newer
  UMG 96RM-E ships with FC 04 from its own document set; that is NOT adopted here as
  evidence for this device.)

## What IS verified (line-by-line; "Frequently required readings" block, manual page 17, plus pf_total from page 18)

| Point | Address | Format | Unit | Note |
| --- | --- | --- | --- | --- |
| v1n / v2n / v3n | 19000 / 19002 / 19004 | FLOAT | V | Voltage L-N |
| v12 / v23 / v31 | 19006 / 19008 / 19010 | FLOAT | V | Voltage L1-L2 / L2-L3 / L1-L3 |
| i1 / i2 / i3 / i_n | 19012 / 19014 / 19016 / 19018 | FLOAT | A | i_n is the vector sum IN=I1+I2+I3 |
| p1 / p2 / p3 / p_total | 19020 / 19022 / 19024 / 19026 | FLOAT | W | Real power; sum Psum3 |
| s1 / s2 / s3 / s_total | 19028 / 19030 / 19032 / 19034 | FLOAT | VA | Apparent power; sum Ssum3 |
| q1 / q2 / q3 / q_total | 19036 / 19038 / 19040 / 19042 | FLOAT | var | Fundamental reactive power; sum Qsum3 |
| pf1 / pf2 / pf3 | 19044 / 19046 / 19048 | FLOAT | - | CosPhi per phase (fundamental component) |
| pf_total | 826 | FLOAT | - | Sum; CosPhisum3 = P0sum3/Ssum3 (from the Measured-values block, page 18) |
| freq | 19050 | FLOAT | Hz | Measured frequency |
| kwh_import | 19068 | FLOAT | Wh -> kWh (scale 0.001) | Real energy L1..L3, consumed, rate 1 |
| kwh_export | 19076 | FLOAT | Wh -> kWh | Real energy L1..L3, delivered |
| kvah_total | 19084 | FLOAT | VAh -> kVAh | Apparent energy L1..L3 |
| kvarh_total | 19092 | FLOAT | varh -> kvarh | Reactive energy L1..L3 |

The PF points are fundamental-component power factors (cos phi). The 800-block also
carries true "Power factor" values (828-834, including harmonic effects) - excluded here
for consistent semantics; use them only if you specifically need total PF.

## Excluded (scope named, per evidence rules)

- "Energy, type Integer" (manual page 34): 64-bit long64 counters; the driver has no 64-bit
  integer type. The FLOAT energy block above covers energy without 64-bit reads.
- Per-phase energy counters (19054-19066, 19070-19074, ...), tariffs other than rate 1,
  rotation field (19052), THD harmonics (19110-19120), mean/min/max value blocks, Fourier
  analysis, limit-value monitoring, and all RD/WR parameter registers (addresses 0-999
  include writable configuration - this template never writes).
- UMG 96RM-E is a DIFFERENT device with its own address list - not covered by this template.

## Hardware validation

The read function code remains an installer choice (above). After choosing them, compare SEVERAL
independent known readings (for example frequency, one voltage and one current) with the
meter's own display - a single plausible value is only a smoke check, it does not prove
the function code, and says nothing about the other registers. Until then,
hardware-untested.
