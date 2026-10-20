# Schneider Electric Acti9 iEM3000 series (Modbus RTU models)

Evidence (vendor domain only):
- Register list: "iEM3000 modbus register.pdf" (1.78 MB), the official attachment on
  Schneider's FAQ "Modbus register address list for iEM3000 series meters.":
  https://www.se.com/in/en/faqs/FA370395/ (Published 28/2/2019, Last Modified 30/8/2024,
  range: iEM3000 series / iEM2000 series), file served from
  https://se.my.site.com/ckmContent/sfc/servlet.shepherd/document/download/0698V00000alUpKQAU
  The PDF is the "iEM3000 series user manual" extract DOCA0005EN-07, chapter
  "Communications via Modbus" (manual pages 37-51). Every table cell below was verified by
  direct pixel inspection of the rendered vendor PDF pages, not OCR or third-party mirrors.
- Vendor-stated conventions (same chapter): the function list contains only FC 03 (Read
  Holding Registers), FC 16 (Write Multiple Registers) and FC 43/14 (Read Device
  Identification) - "To read different parameters from the meter, use function 3 (Read)".
  The table format defines Register Address as "Modbus address of register encoded in the
  Modbus frame, in decimal (dec)", so the listed number goes on the wire unmodified.

## Models (manual, "Modbus communication overview")

Modbus RTU is available on iEM3150, iEM3155, iEM3250, iEM3255, iEM3350, iEM3355, iEM3455
and iEM3555. Serial settings are configured on the meter display: 9600 / 19200 / 38400
baud, even / odd / no parity, number of stop bits = 1, address 1-247. This document states
no factory serial default - match the edge setup form to the meter's actual settings.

## What IS verified (line-by-line against the register list, "Meter Data" section)

Every template point's register number, 2-register size, FLOAT32 type, unit and
description (all read via FC 03, Action R):

| Point | Register (as listed) | Type | Unit | Manual description |
| --- | --- | --- | --- | --- |
| i1 / i2 / i3 | 3000 / 3002 / 3004 | Float32 | A | I1/I2/I3: phase 1/2/3 current |
| i_avg | 3010 | Float32 | A | Current Avg |
| v12 / v23 / v31 | 3020 / 3022 / 3024 | Float32 | V | Voltage L1-L2 / L2-L3 / L3-L1 |
| v_avg_ll | 3026 | Float32 | V | Voltage L-L Avg |
| v1n / v2n / v3n | 3028 / 3030 / 3032 | Float32 | V | Voltage L1-N / L2-N / L3-N |
| v_avg_ln | 3036 | Float32 | V | Voltage L-N Avg |
| p1 / p2 / p3 | 3054 / 3056 / 3058 | Float32 | kW | Active Power Phase 1/2/3 |
| p_total | 3060 | Float32 | kW | Total Active Power |
| pf_total | 3084 | Float32 | - | Total Power Factor (see encoding below) |
| freq | 3110 | Float32 | Hz | Frequency |
| energy_import | 45100 | Float32 | Wh | Total Active Energy Import (32-bit float block) |

Power factor encoding (manual, register 3084 description): four-quadrant encoding, NOT
plain -1..1 - -1 < PF < 0 = Quad 2 (active power negative, capacitive), -2 < PF < -1 =
Quad 3 (active power negative, inductive), 0 < PF < 1 = Quad 1 (active power positive,
inductive), 1 < PF < 2 = Quad 4 (active power positive, capacitive). Same encoding family
as the PM53xx / EM6400NG+ maps.

## Excluded (scope named, per evidence rules)

- Total Reactive Power (3068) and Total Apparent Power (3076): the register list marks
  them "Not applicable for iEM3150 / iEM3250 / iEM3350", so they cannot serve all named
  models and are left out.
- Energy export (45102 float / 3208 Int64), reactive energy (45104/45106, 3220/3224),
  partial energy, input metering: same model-applicability exclusion for the x50 models.
- Energy by tariff (4196-4208 Int64 / 45120-45126 float): iEM3155 / iEM3255 / iEM3355 only.
- The Int64 total-energy block (3204 Total Active Energy Import Wh and neighbours): the
  edge driver has no 64-bit integer type; the parallel 32-bit Float32 block at 45100+
  carries the same values in Wh and is used instead.
- Per-phase energy (45112/114/116), demand, overload alarm, digital I/O and the command
  interface (registers 5250-5499): not needed for the base template; writes are never
  emitted by templates anyway.

## What is NOT stated (installer chooses; no trusted default)

The document nowhere states the word order of the 2-register Float32 values, so the
template gates YAML generation on an explicit word-order choice
(wordOrderUnverified). Validate against the real meter with SEVERAL independent known
readings (for example frequency, one voltage and one current) compared with the meter's
own display - a single plausible value is a smoke check only and proves nothing about the
other registers. Until that comparison is done, values from this template are
hardware-untested.
