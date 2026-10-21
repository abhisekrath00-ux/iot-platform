# Kamstrup MULTICAL 403/603/803 (thermal energy meter, Modbus RTU/TCP module)

Evidence (vendor domain only):
- Data sheet: "Modbus RTU - Modbus/KMP TCP/IP, MULTICAL 403 / 603 / 803 - Modbus register
  mapping, Registers for PQT Controller", document ID FILE100004479_C_EN_03.2025, retrieved
  from Kamstrup's Sitecore Content Hub delivery tenant:
  https://kamstrup-delivery.sitecorecontenthub.cloud/api/public/content/61072-downloadOriginal?v=10de6c70
  Provenance chain for that host (a bare domain match would not be enough), verified live in a
  browser session on 2026-10-21: kamstrup.com's own MULTICAL 403 product page serves ALL of its
  document download links from kamstrup-delivery.sitecorecontenthub.cloud
  (/api/public/content/<id>-downloadOriginal), kamstrup.com itself is served from Kamstrup
  A/S's Sitecore production tenant (edge.sitecorecloud.io/kamstrupas-kamstrup-prd-...), and
  <tenant>.sitecorecontenthub.cloud delivery subdomains are assigned by Sitecore to the tenant
  customer - a public delivery link can only be published from that tenant's own Content Hub.
  The PDF metadata (Adobe InDesign production, April 2025) matches the document ID's
  03.2025 dating.
  Every row used below was verified by direct pixel inspection of the rendered vendor PDF
  (pages 3-5 and 8) - not OCR, not third-party mirrors.
- Covers the Modbus modules HC-003-67 (Modbus RTU) and HC-003-82 (Modbus/KMP TCP/IP).
  "Any datagrams can be used in any of the MULTICAL XX3" - the default datagram xx-yy-300
  used here applies to all three models; registers a given model/configuration lacks
  answer with the documented invalid value (below), they do not error.

## Vendor-stated conventions

- Register numbering vs hex address: the table legend states "The Modbus register count
  starts at number 1, and corresponds to the memory address 0." - the printed register N
  and the printed hex address (N-1) are the vendor's two numberings of the SAME cell. The
  data sheet never says which one is sent on the wire, so the base stays an installer
  choice (gate below); the template stores the printed register numbers and the gate's
  minus-1 choice yields the printed hex addresses.
- Sizes/types: values are 32-bit IEEE float at the addresses used here; units and factors
  are 16-bit; a parallel 32-bit signed-integer block (registers 154+, with 10^factor
  scaling registers at 245+) exists and is NOT used by this template.
- Unit/factor scheme: every value's SI unit is set by the meter configuration and
  readable in the unit registers (83+): Flow V1 83, Actual Power 85, t1 86, t2 87,
  t1-t2 diff 90, Heat energy E1 93, Cooling energy E3 95, Volume V1 109. Unit codes:
  Wh=1, kWh=2, MWh=3, GWh=4, W=21, kW=22, MW=23, degC=37, Kelvin=38, l=39, m3=40,
  l/h=41, m3/h=42, bar=52. (Factor registers apply only to the unused integer block.)
- Invalid value: a register the attached meter does not have returns the documented
  invalid float 0x4F800000 (about 4.3e9) - a wildly out-of-range reading means "not
  present on this meter/configuration", not a wiring fault.

## NOT vendor-stated (installer gates, required before YAML)

- Word order: the data sheet never states the byte/word order of the 32-bit floats.
  Choose from the module's documentation or by comparing one reading (for example t1)
  against the meter display.
- Read function code: "function code 3 or 4" appears only in the PQT-controller section;
  the main map is not bound. Choose FC 03 or FC 04 yourself.
- Register base: which of the two printed numberings (register N vs hex address N-1)
  goes on the wire. Choose from the module's documentation or by testing.
- Configured units (not a selector, part of the confirmation): this template labels
  energy kWh, power kW, flow m3/h, volume m3 and temperatures degC. Read the unit
  registers listed above (or check the meter display / METERTOOL) and confirm the meter
  reports those units; with any other configured unit every affected value is off by the
  unit ratio.

## What IS verified (line-by-line; default datagram xx-yy-300, float values)

| Point | Modbus Register | Address (hex) | Size | Unit (as configured) | Description |
| --- | --- | --- | --- | --- | --- |
| flow_m3h | 1 | 0000 | 2 | m3/h | Flow V1 actual |
| power_kw | 5 | 0004 | 2 | kW | Actual Power |
| t_flow_c | 7 | 0006 | 2 | degC | t1 actual |
| t_return_c | 9 | 0008 | 2 | degC | t2 actual |
| t_diff_k | 15 | 000E | 2 | K | t1-t2 diff. temp. |
| heat_kwh | 21 | 0014 | 2 | kWh | Heat energy E1 |
| cooling_kwh | 25 | 0018 | 2 | kWh | Cooling energy E3 (cooling-configured meters) |
| volume_m3 | 53 | 0034 | 2 | m3 | Volume V1 |

## Excluded (scope named, per evidence rules)

- Flow V2, t3/t4, P1/P2 pressures, energies E2 and E4-E11, tariffs TA2-TA4 and limits,
  heat energies A1/A2, Volume V2, pulse inputs, COP, mass M1/M2, info/error/operating
  hours, date/time/config/customer/serial registers, the integer value block (154+) with
  its factor registers (163+), the legacy datagram xx-yy-301, the MULTICAL 803-only
  datagram xx-yy-302, and all PQT-controller set points (writable - this template never
  writes).

## Hardware validation

Three conventions remain installer choices plus the unit confirmation (above). After
choosing, compare SEVERAL independent known readings (for example t1 temperature, flow
and heat energy) with the meter's own display - a single plausible value is only a smoke
check, it does not prove the word order, function code, base or units, and says nothing
about the other registers. Until then, hardware-untested.
