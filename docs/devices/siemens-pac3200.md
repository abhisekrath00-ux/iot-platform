# Siemens SENTRON PAC3200 (three-phase power monitoring device)

Source: the manufacturer's "SENTRON Power Monitoring Device SENTRON PAC3200" manual,
document A5E01168664B-04 (02/2008 edition, cache.industry.siemens.com, fetched 2026-10-20),
table 3-6 "Available measured variables". Nothing here has been run against a real meter.

## Link

Modbus TCP on the base unit's Ethernet port (the transmission protocol must be switched from
SEAbus TCP to Modbus TCP on the device, manual section 3.9.2). All table 3-6 values are
readable with function codes 0x03 and 0x04; this template uses FC 04. Register numbers below
are the manual's "Offset" column, used verbatim. Addressing IS verified: Siemens' own
application example "Data Exchange between S7-1200 and SENTRON PAC via MODBUS TCP" (Entry ID
40614428, table 3-12) reads total active power at MB_DATA_ADDR 40001+65 and the operating
hours counter at 40001+213, so the on-wire PDU address equals the table 3-6 offset exactly.

## Value formats

Instantaneous values (offsets 1-73) are IEEE-754 Float (2 registers), counters at 213/215 are
unsigned long (2 registers), energy counters (offsets 801-837) are IEEE-754 Double (4
registers). The manual does not state the register word order; standard big-endian, most
significant register first (`abcd`), is assumed. For the 2-register Float values this matches
the S7 example's 'Real' overlay. For the 4-register Double energy counters the order is NOT
verified anywhere official, which is why this template is marked provisional in the UI: the
platform refuses to generate a device config until the user confirms the assumed order
against the meter (compare one known energy reading). If energy values decode as nonsense,
try `cdab`.

## Measurements (read-only, FC 04)

| Point | Offset | Type | Unit |
|---|---|---|---|
| va_n, vb_n, vc_n | 1, 3, 5 | Float | V |
| va_b, vb_c, vc_a | 7, 9, 11 | Float | V |
| ia, ib, ic | 13, 15, 17 | Float | A |
| sa, sb, sc | 19, 21, 23 | Float | VA |
| pa, pb, pc | 25, 27, 29 | Float | W |
| qa, qb, qc | 31, 33, 35 | Float | var |
| pfa, pfb, pfc | 37, 39, 41 | Float | - (0..1 per manual; lead/lag sign convention not stated) |
| thd_va..c, thd_ia..c | 43-53 | Float | % |
| freq | 55 | Float | Hz (45..65) |
| avg_vpn, avg_vpp, avg_i | 57, 59, 61 | Float | V / V / A |
| s_total, p_total, q_total, pf_total | 63, 65, 67, 69 | Float | VA / W / var / - |
| unbal_v, unbal_i | 71, 73 | Float | % |
| work_hours | 213 | u32 | s |
| universal_counter | 215 | u32 | - |
| energy_import_t1, energy_import_t2 | 801, 805 | Double | Wh |
| energy_export_t1, energy_export_t2 | 809, 813 | Double | Wh |
| reactive_import_t1, reactive_import_t2 | 817, 821 | Double | varh |
| reactive_export_t1, reactive_export_t2 | 825, 829 | Double | varh |
| apparent_t1, apparent_t2 | 833, 837 | Double | VAh |

## Deliberately excluded

- Offsets 75-211: min/max history values (same names, "Maximum/Minimum ..." rows of table
  3-6). Left out to keep the default poll light; add them by hand or CSV import if needed.
- Offset 217-221: parameter-change and limit-violation counters.
- Everything marked RW in the manual (counter resets, and the 50xxx settings registers such
  as CT ratios and demand period). Industrial onboarding is read-only by default; writes are
  never inferred from a map.
- Digital input/output status (offsets 200/300, function code 0x02): bit-packed, not yet
  represented in the template model.
