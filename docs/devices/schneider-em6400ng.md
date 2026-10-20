# Schneider Electric EasyLogic EM6400NG+ (RS-485 models)

Evidence (vendor domain only):
- Register list: "Public_EM6400_PM2xxx PMC Register List_v1050_6.xls" (950.5 KB), the official
  attachment on Schneider's FAQ "Modbus Register address required for EM6400 meter?":
  https://www.se.com/in/en/faqs/FA349907/ (Last Modified 26/7/2024, product METSEEM6400NGRSCL2),
  file served from
  https://se.my.site.com/ckmContent/sfc/servlet.shepherd/document/download/0698V00000jcmBaQAI
  The workbook has an explicit "EM6400 NG" availability column - every template point below is Y.
- User manual: "EasyLogic EM6400NG+ User Manual" NHA2768503-07 (05/2022), served from
  https://download.se.com/files?p_Doc_Ref=NHA2768503%20-%2000
  The manual itself carries no register addresses: "See your meter's Modbus register list at
  www.se.com for the Modbus mapping information" (Modbus command interface section).
- The same FAQ also attaches the 2006 Conzerv-era EM6400 series map
  (EM6400RegMap_V01.01.02.pdf, 4xxxx float addresses for the discontinued legacy models). It is
  NOT used: it predates the NG+ and names only legacy models. Third-party mirrors claiming an
  "EM6400NG offset registers map" (e.g. a Scribd upload) were treated as untrusted leads only.

## Models (user manual, "Meter types")

METSEEM6400NGRSCL1 (Class 1), METSEEM6400NGRSCL5 (Class 0.5S), METSEEM6400NGRSCL2 (Class 0.2) -
RS-485 + display. The POCL1/POCL5 variants are pulse-output only (no RS-485) and are excluded.

Factory serial defaults (user manual, communications setup): 19200 baud, even parity
(4800/9600/19200/38400 and even/odd/none selectable). Unit ID is installer-set.

## What IS verified (line-by-line against the register list, sheet "Register List")

Every template point's register number, data type, 2-register size, unit, and EM6400 NG
availability (Y):

| Point | Register (as listed) | Type | Unit | Workbook description |
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
| freq | 3110 | FLOAT32 | Hz | Frequency |
| kwh_import | 2700 | FLOAT32 | kWh | Active Energy Delivered (Into Load), Accumulated Energy block |

Cell values were machine-read from the original XLS (xlrd), not OCR; the "EM6400 NG" header
cell and each Y cell are direct reads, so no column-layout inference is involved.

Excluded: the parallel "Accumulated Energy Permanent" block (same values at 2676+,
non-resettable - the resettable 2700 block matches the PM53xx template's choice); INT64 energy
counters (driver has no u64/i64); demand, harmonics, min/max and all command/setup registers
(Commands sheet, registers 5000+): the template is read-only.

## Power factor is four-quadrant encoded (vendor-stated, sheet "Data Types")

Schneider's 4Q_FP_PF range is -2..2, NOT plain -1..1. Vendor pseudocode: if reg > 1 then
PF = 2 - reg (leading); if reg < -1 then PF = -2 - reg (leading); if |reg| = 1 then unity;
otherwise PF = reg (lagging). The template exposes the raw encoded value with guardrails
-2..2; consumers must apply the quadrant decode. This is why the template's pf_total max is 2.

## What is NOT stated in any Schneider document we could obtain

1. Multi-register word order for FLOAT32. The workbook's Data Types sheet defines FLOAT32 but
   not its register order; the manual defers to the register list. -> The template forces an
   explicit installer word-order choice (wordOrderUnverified gate); no default is trusted.
2. Register base (PDU addressing). The list numbers registers from 1 and neither the list nor
   the manual says whether the on-wire address is the listed number or the listed number
   minus 1. -> The template forces an explicit base choice (pduBaseUnverified gate).
3. Read function code. No FC statement in either document. The template forces an explicit
   choice (funcUnverified gate): FC 03 (read holding registers) or FC 04 (read input
   registers); the emitted YAML carries the choice. No default is trusted.

Validation path for an installer (also in the template text): after choosing, compare SEVERAL
independent known readings (for example frequency, one voltage and one current) against the
meter's own display. A single plausible value is only a smoke check - it does not prove the
address, word order or function code, and says nothing about the other registers. Until that
display comparison is done, every value from this template is hardware-untested.

Min/max guardrails in the template (0-1000 V, 0-10000 A, 40-70 Hz, etc.) are platform sanity
ranges, not vendor-stated limits: the workbook's native FLOAT32 range is +/-1e38.

Never run on a real meter without the installer's own verification.
