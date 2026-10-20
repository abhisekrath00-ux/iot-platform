# Schneider Electric PowerLogic PM53xx (PM5310/5320/5330/5331/5340/5341)

Evidence (vendor domain only):
- Register list: "PM51xx_PM53xx_PMC Register List_v2011_v2021_R01.xls" (881.5 KB), the official
  attachment on Schneider's FAQ "PowerLogic PM5000 Series Modbus Register List":
  https://www.se.com/us/en/faqs/FA234017/ (Last Modified 6/20/2025), file served from
  https://se.my.site.com/ckmContent/sfc/servlet.shepherd/document/download/069Kj00000TH2LXIA1
- User manual: "PM5300 User Manual" EAV15107-EN11:
  https://productinfo.se.com/pm5300/5be97f3b347bdf0001d99c87/PM5300%20User%20Manual/English/EAV15107-EN11.pdf

## What IS verified (line-by-line against the register list, sheet "Register List")

Every template point's register number, FLOAT32 type, 2-register size, unit, and per-model
availability (Y for PM5310, PM5320, PM5330_31, PM5340_41):

| Point | Register (as listed) | Type | Unit | Workbook row |
| --- | --- | --- | --- | --- |
| i1 / i2 / i3 / i_n | 3000 / 3002 / 3004 / 3006 | FLOAT32 | A | Current A/B/C/N |
| i_avg | 3010 | FLOAT32 | A | Current Avg |
| v12 / v23 / v31 | 3020 / 3022 / 3024 | FLOAT32 | V | Voltage A-B/B-C/C-A |
| v_avg_ll | 3026 | FLOAT32 | V | Voltage L-L Avg |
| v1n / v2n / v3n | 3028 / 3030 / 3032 | FLOAT32 | V | Voltage A-N/B-N/C-N |
| v_avg_ln | 3036 | FLOAT32 | V | Voltage L-N Avg |
| p1 / p2 / p3 / p_total | 3054 / 3056 / 3058 / 3060 | FLOAT32 | kW | Active Power A/B/C/Total |
| q_total | 3068 | FLOAT32 | kVAR | Reactive Power Total |
| s_total | 3076 | FLOAT32 | kVA | Apparent Power Total |
| pf_total | 3084 | 4Q_FP_PF (FLOAT32) | - | Power Factor Total |
| freq | 3110 | FLOAT32 | Hz | Frequency (also in the user manual's own table) |
| kwh_import | 2700 | FLOAT32 | kWh | Active Energy Delivered (Into Load) |

Excluded: all INT64 Wh/VARh/VAh counters (3204-3251, 4196+) - the edge driver has no u64/i64;
the FLOAT32 kWh counter at 2700 is used instead. Commands (write) registers 5000+ are excluded:
the template is read-only.

## Power factor is four-quadrant encoded (vendor-stated, sheet "Data Types")

Schneider's 4Q_FP_PF range is -2..2, NOT plain -1..1. Vendor pseudocode: if reg > 1 then
PF = 2 - reg (leading); if reg < -1 then PF = -2 - reg (leading); otherwise PF = reg (lagging,
unity at +/-1). The template exposes the raw encoded value with guardrails -2..2; consumers
must apply the quadrant decode. This is why the template's pf_total max is 2.

## What is NOT stated in any Schneider document we could obtain

1. Multi-register word order for FLOAT32. The workbook's Data Types sheet defines FLOAT32 but
   not its register order; the manual defers to the register list. -> The template forces an
   explicit installer word-order choice (wordOrderUnverified gate); no default is trusted.
2. Register base (PDU addressing). The list numbers registers from 1 (first row: register 1)
   and neither the list nor the manual says whether the on-wire address is the listed number or
   the listed number minus 1. -> The template forces an explicit base choice
   (pduBaseUnverified gate): "listed numbers as-is" or "listed numbers minus 1".
3. Read function code. No FC statement in either document. The template uses FC 03 (read
   holding registers), the only sensible reading of a "register list"; if the meter rejects
   FC 03, that is visible immediately (exception 01) and the doc must be revisited.

Practical validation path for an installer (also in the template text): read frequency (listed
at 3110). A sane Hz value confirms word order, base, and FC in one reading.

Min/max guardrails in the template (0-1000 V, 0-10000 A, 40-70 Hz, etc.) are platform sanity
ranges, not vendor-stated limits: the workbook's native FLOAT32 range is +/-1e38.

Decode fixtures: edge/internal/driver/modbus_pm53xx_test.go (FC 03 assert, IEEE-754 round-trip
incl. negative power, 4Q PF raw value, listed-minus-1 base decoding the same quantity). These
validate our arithmetic against the template's stated formats; they are not hardware claims.
Never run on a real meter without the installer's own verification.
