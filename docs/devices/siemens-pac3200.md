# Siemens SENTRON PAC3200 (three-phase power monitoring device)

Source: the manufacturer's "SENTRON Power Monitoring Device SENTRON PAC3200" manual,
document A5E01168664B-04 (02/2008 edition, cache.industry.siemens.com, fetched 2026-10-20),
table 3-6 "Available measured variables". Nothing here has been run against a real meter.

## Link

Modbus TCP on the base unit's Ethernet port (the transmission protocol must be switched from
SEAbus TCP to Modbus TCP on the device, manual section 3.9.2). All table 3-6 values are
readable with function codes 0x03 and 0x04; this template uses FC 04. Register numbers below
are the manual's 1-based "Offset" column, used verbatim. The manual does not say whether a
master must subtract one to get the on-wire PDU address; if reads come back shifted by one
register, subtract 1 from every register in this map. Not verified on hardware.

## Value formats

Instantaneous values (offsets 1-73) are IEEE-754 Float (2 registers), counters at 213/215 are
unsigned long (2 registers), energy counters (offsets 801-837) are IEEE-754 Double (4
registers). The manual does not state the register word order; standard big-endian, most
significant register first (`abcd`), is assumed and flagged here as unverified. If values
decode as nonsense on a real meter, try `cdab` first.

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
