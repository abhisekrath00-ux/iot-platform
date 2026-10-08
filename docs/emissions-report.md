# Emissions section (Scope 1 and 2) for reports

An optional section in any report that turns metered quantities into tonnes of CO2e: Scope 2 for purchased electricity (kWh meters), Scope 1 for fuel burned on site (litres, kg, m3 of fuel from a tank or flow meter).

**What it is not.** It is a calculation aid. It is not a certified or assured BRSR or GHG Protocol report, it does not produce the SEBI BRSR filing format, and it has no Scope 3. Every report that carries the section prints that notice (HTML, CSV, Excel, PDF, Word, PowerPoint).

**No emission factors are built in.** Grid and fuel factors change every year and differ by country, state and supplier, so the platform ships none. For each source you enter the factor (kg CO2e per unit) and where it comes from (publication and year, up to 120 characters). Both are required and both are printed in the report. Use the factor your auditor or regulator expects.

## How a source is calculated

- `scope`: 1 or 2. `metric`: a point that is also one of the report's metrics. `unit`: what the quantity is measured in (kWh, L...). `scale` (optional, default 1) converts the reading to the factor's unit.
- `mode: sum` (default): readings are amounts per interval (for example kWh used in each reading). The quantity is the sum over the window.
- `mode: delta`: readings are a running counter. The quantity is the last bucket's maximum minus the first bucket's minimum. A counter that goes backwards (reset) is listed with a note and counted as zero, never hidden. Usage before the first reading in the window is not counted.
- tCO2e = quantity x scale x factor / 1000. Scope 1 total, Scope 2 total and Scope 1 + 2 total are shown. A source with no data is listed as "no data" and counts as zero.
- Optional `emissions_intensity` {label, metric, mode, scale} divides the total by a denominator point (production units, for example) and prints tCO2e per that unit. API only for now; the Reports page form has the sources, not the intensity line.

## Use

Reports page > "Emissions, Scope 1 and 2" > Add emission source. Or in the report definition JSON: `"emissions":[{"name":"Grid electricity","scope":2,"metric":{"device_id":"meter-1","point_id":"kwh"},"unit":"kWh","factor":<your factor>,"factor_source":"<where it is from>"}]`. At most 20 sources. Validation rejects a missing or non-positive factor, a missing source text, a metric not in the report, and a bad scope or mode.

## Tested and not tested

Tested: the arithmetic with made-up factors (sum and delta modes, scale, intensity), gaps and counter resets, validation, and the section present in HTML, CSV, XLSX, DOCX, PPTX and PDF; a real report created through the API on a database with seeded readings and downloaded in all six formats, PDF page and form screenshots inspected.
NOT tested: against a real plant's meters, with real emission factors, or against any regulator's template or an auditor's expectations. Charts of emissions over time are not included. The Excel, Word and PowerPoint layouts were checked by file structure and text, not opened in Microsoft Office.
