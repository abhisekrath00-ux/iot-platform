# Siemens SENTRON PAC4200 (three-phase power monitoring device)

Sources (both Siemens AG domains, fetched 2026-10-20):
- "SENTRON PAC4200 System Manual", A5E02316180B-03 (09/2010 edition,
  cache.industry.siemens.com/dl/files/595/34261595/att_920400/v1/manual_sentron_pac4200_03_en-US.pdf),
  appendix A.3 MODBUS, tables A-1 (measured variables) and A-14 (tariff-specific energy, float).
- "SENTRON PAC RS485 expansion module Manual", A5E02091800B-01 (02/2008 edition,
  cache.industry.siemens.com/dl/files/965/28865965/att_37895/v1/SENTRON_PAC_RS485-Modul_Manual_01_US.pdf),
  section 5.1 MODBUS RTU and section 12.1 "Examples of MODBUS communication".
Nothing here has been run against a real meter.

## Link

Modbus TCP on the base unit's Ethernet port (manual section: communication over MODBUS TCP),
or Modbus RTU through the PAC RS485 expansion module - the module manual documents the same
offset map and the device can act as a Modbus TCP-to-RTU gateway. All measured variables in
table A-1 and the float energy values in table A-14 are readable with function codes 0x03 AND
0x04 (stated in both section headers); this template uses FC 04.

## Addressing (verified)

Register numbers below are the manual's "Offset" column, used verbatim, and the on-wire PDU
address equals the listed offset exactly. Evidence: the PAC RS485 module manual's own frame
examples (section 12.1) read "From offset 50001" with frame bytes C3 51 (= 50001 decimal) and
"From offset 60006" with frame bytes EA 66 (= 60006 decimal) - the listed offset goes on the
wire unmodified. Both offsets also appear in the PAC4200 system manual's own tables (50001 =
connection type, 60006 = switch tariff), confirming the two manuals describe the same map.

## Value formats and the ONE unverified convention

All values in this template are IEEE-754 Float (2 registers each). NEITHER Siemens document
states the multi-register byte/word order anywhere we could find. There is no trusted default:
the UI makes the installer choose the word order explicitly (from their exact device's
behaviour, validated by comparing several known readings - frequency, one voltage, one
current - against the meter's own display) before any YAML is generated. A single plausible
reading is only a smoke check.

Date/Time at offset 2799 (unsigned long) is excluded. Maximum/minimum history tables (offsets
75+ in A.3.8/A.3.9) are excluded. Double-format energy table A.3.6 is excluded - the same
counters exist as Float in table A.3.7, which this template uses.

## Measurements (read-only, FC 04, offsets from table A-1)

| Point | Offset | Type | Unit |
|---|---|---|---|
| va_n, vb_n, vc_n | 1, 3, 5 | Float | V |
| va_b, vb_c, vc_a | 7, 9, 11 | Float | V |
| ia, ib, ic | 13, 15, 17 | Float | A |
| sa, sb, sc | 19, 21, 23 | Float | VA |
| pa, pb, pc | 25, 27, 29 | Float | W |
| qa, qb, qc | 31, 33, 35 | Float | VAR |
| pfa, pfb, pfc | 37, 39, 41 | Float | - (0..1 per table) |
| thd_v_ab, thd_v_bc, thd_v_ca | 43, 45, 47 | Float | % (line-to-line THD; offsets 49-53 are marked Reserve) |
| freq | 55 | Float | Hz (45..65 per table) |
| avg_vpn, avg_vpp | 57, 59 | Float | V |
| avg_i | 61 | Float | A |
| s_total | 63 | Float | VA |
| p_total | 65 | Float | W |
| q_total | 67 | Float | VAR |
| pf_total | 69 | Float | - (value range not stated; template allows -1..1) |
| unbal_v, unbal_i | 71, 73 | Float | % |

## Energy (read-only, FC 04, Float, from table A-14; tariff 1 = on-peak, tariff 2 = off-peak)

| Point | Offset | Type | Unit |
|---|---|---|---|
| energy_import_t1, energy_import_t2 | 2801, 2803 | Float | Wh |
| energy_export_t1, energy_export_t2 | 2805, 2807 | Float | Wh |
| reactive_import_t1, reactive_import_t2 | 2809, 2811 | Float | VARh |
| reactive_export_t1, reactive_export_t2 | 2813, 2815 | Float | VARh |
| apparent_t1, apparent_t2 | 2817, 2819 | Float | VAh |

Overflow at 1.0e+12 per the table. Process-energy counters (2821-2831) and the Date/Time
register (2799) are excluded.
