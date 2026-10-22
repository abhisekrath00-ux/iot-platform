# Finder 7M.24 (single-phase DIN-rail energy meter/analyzer) - Modbus register map provenance

Template `finder-7m24` in `web/src/lib/edgeSetup.ts`. Never run on a real meter; the edge
agent only reads.

## Sources (vendor domain only)

- Register map + worked examples: "ModBUS COMMUNICATION PROTOCOL 7M24 - 7M38"
  (Modbus-7M24-7M38_v2_30062021.pdf, 58 pages), 7M.24 section ("PROTOCOL 7M24" starts at
  footer page 3; the actual-measurements table is footer page 5, pixel-verified), from
  https://cdn.findernet.com/app/uploads/Modbus-7M24-7M38_v2_30062021.pdf
- Factory serial defaults: 7M.24 user manual, specification table. The EN edition
  (manual7M24_EN.pdf) returns HTTP 403 on the vendor CDN, so the official IT edition was
  used (manuale7M24_IT.pdf) - the spec rows are numeric and language-independent
  (Velocita di trasmissione / Frame / Protocollo / Indirizzo), from
  https://cdn.findernet.com/app/uploads/manuale7M24_IT.pdf

No third-party copies were used. This is the single-phase sibling of the 7M.38 template;
both use the same protocol document and the same decoder family.

## Conventions - all vendor-stated

1. Read function: FC 04 input registers ("The memory reference for input and holding
   registers is 30000 and 40000 respectively"; the table's read column states 4 for
   every row).
2. Register base: on-wire (PDU) address = listed number minus 30000, demonstrated by the
   protocol's own request-frame example (U1 listed 30107, request address bytes
   `00 6B` = 107). The example also uses slave address 0x21 = 33 - the factory default.
3. Word order: T5 decoding example prints `123456*10^-3` as `FD01 E240`, register one
   first. MSW first; template word order `abcd`.
4. Data types: T5 = unsigned decade-exponent measurement (signed 8-bit exponent in byte 0,
   unsigned 24-bit mantissa in bytes 1-3); T6 = same with signed mantissa. Same encoding
   the edge agent already decodes as `t5`/`t6` for the Iskra and Finder 7M.38 templates;
   Finder's worked example `FD01 E240 = 123456 x 10^-3` is bit-identical to the
   decoder's test vector. T16 = unsigned 16-bit with 2 decimal places (scale 0.01).
   Decoded T5/T6 values are already physical values in base units.
5. Factory serial settings (7M.24 manual spec table): RS485, speed 1200-115200 bit/s
   (default 19200), Frame 8,N,2 (no parity, TWO stop bits), Protocol MODBUS RTU, default
   address 33. Surfaced as a hint with an explicit Apply button; stop bits preset to 2;
   never silently applied.
6. Source caveat: the printed byte example in the request-frame page is internally
   inconsistent (its bytes do not decode to its printed value). The T5 type-definition
   example is unambiguous and is the one the decoder is tested against.

## Point map (7M.24 actual-measurements table, on-wire = listed - 30000)

| Point | Listed | On-wire | Type | Decodes to |
| --- | --- | --- | --- | --- |
| freq | 30105 | 105 | T5 | Hz |
| v1 | 30107 | 107 | T5 | V |
| v_avg_ln | 30113 | 113 | T5 | V |
| i1 | 30126 | 126 | T5 | A |
| i_avg | 30136 | 136 | T5 | A |
| si | 30138 | 138 | T5 | A |
| p_total / p1 | 30140 / 30142 | 140 / 142 | T6 | W |
| q_total / q1 | 30148 / 30150 | 148 / 150 | T6 | var |
| s_total / s1 | 30156 / 30158 | 156 / 158 | T5 | VA |
| thd_v1 | 30182 | 182 | T16 | % (raw x 0.01) |
| thd_i1 | 30188 | 188 | T16 | % (raw x 0.01) |

14 points. All FC 04.

## Excluded - and why

- **Power factor (PFt 30164, PF1 30166)**: T7 packs import/export and inductive/capacitive
  as two separate sign bytes - no single signed PF number is vendor-stated. Same stance
  as the 7M.38 template.
- **ALL energy counters**: the 7M user manual states the meter's energy registers are
  user-parameterisable for energy type, active quadrants, counting direction and tariff,
  and each T3 counter's scale lives in a separate runtime-changeable exponent register.
- Run time (T3 + separate exponent), phase angles and internal temperature (T17), I/O
  status and alarm registers, and ALL setup/configuration registers (the edge agent is
  read-only by design).

## Tested vs untested

Tested: register-evidence vitest asserts every on-wire address, type, the THD 0.01 scale,
the serial hint, no overlapping spans, and the FC 04 YAML. Full web suite and production
build green. Picker pixel-verified on desktop and mobile.

NOT tested: real hardware. Compare one reading (for example frequency or the voltage)
against the meter's own display before trusting values.
