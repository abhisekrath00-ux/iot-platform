# Socomec DIRIS A-10 (RS485 variant 48250401) - Modbus register map provenance

Template `socomec-diris-a10` in `web/src/lib/edgeSetup.ts`. Never run on a real meter; the
edge agent only reads.

## Sources (vendor domain only)

- Register map: Socomec's official communication table "MODBUS tables of Diris A-10",
  CMT_EN 2025-02 (HTML), from
  https://emea.socomec.com/sites/default/files/2025-02/DIRIS-A-10---MULTIFUNCTION-METER_COMMUNICATION-TABLE_2025-02_CMT_EN_0.html
- Communication interface: "DIRIS A-10 Power Monitoring Device PMD Quick Start Guide"
  538847_E (PLURI), from
  https://www.socomec.us/sites/default/files/2026-01/DIRIS-A-10---Power-Monitoring-Device---P_QUICK-START-GUIDE_2026-01-29-13-23-58_538847_E_MULTI_PLURI_8.pdf
- Variant note: technical guide 548052 (A-10/A-20/A-30/A-41) confirms RS485 Modbus is the
  COM module; the QSG states order 48250400 = without COM RS485, 48250401 = with COM RS485.

No third-party copies were used. The Phoenix Contact EMpro register table was considered
first for this slot and abandoned: its register table download sits behind an
Akamai-blocked product page (HTTP 403 / "Access Denied" for both direct fetch and a
cloud-browser session) - never use an inaccessible register doc.

## Conventions - what is and is not vendor-stated

1. Stated by the QSG: the RS485 variant speaks JBUS/MODBUS RTU; parity none/odd/even is
   selectable. The communication address, speed, parity and stop bit are all set in the
   meter's own menu (QSG menu flow: Communication address / speed / parity / stop bit).
   No factory defaults are stated anywhere, so the template carries NO serial hint -
   the installer matches the form to the meter.
2. NOT stated anywhere (table, QSG, technical guide): the read function code (FC 03
   holding vs FC 04 input), the multi-register word order for U32/S32, and whether the
   listed decimal address or the listed address minus 1 goes on the wire. All three are
   installer gates in the template (`funcUnverified`, `wordOrderUnverified`,
   `pduBaseUnverified`); the addresses below are stored as listed.
3. Units are printed per row in the table ("V 10-2", "m A", "W 10", "var 10", "VA 10",
   "Hz 10-2", "- / 1000", "% / 10", "k Wh", "varh 10^3", "k VAh") and map to fixed
   scales: 0.01, 0.001, 10, 10, 10, 0.01, 0.001, 0.1, 1 (kWh), 1 (kvarh; the printed
   "varh 10^3" means the counter already counts thousands of varh), 1 (kVAh).
4. PF sign is vendor-stated on the signed rows: "power factor : -: leading et + : lagging"
   (negative = leading/capacitive, positive = lagging/inductive).
5. The Common metrology block heading says "Affected by current and voltage
   transformers" - values are primary-side with the configured CT/VT ratios applied.

## Point map (41 points; addresses listed as printed, base gated)

Common metrology block (U32/S32, 2 words each):
| Point | Address | Type | Scale |
| --- | --- | --- | --- |
| v12 / v23 / v31 | 50514 / 50516 / 50518 | U32 | 0.01 V |
| v1 / v2 / v3 | 50520 / 50522 / 50524 | U32 | 0.01 V |
| freq | 50526 | U32 | 0.01 Hz |
| i1 / i2 / i3 / i_n | 50528 / 50530 / 50532 / 50534 | U32 | 0.001 A |
| p_total / q_total | 50536 / 50538 | S32 | 10 W / 10 var |
| s_total | 50540 | U32 | 10 VA |
| pf_total | 50542 | S32 | 0.001 (sign stated) |
| p1 / p2 / p3 | 50544 / 50546 / 50548 | S32 | 10 W |
| q1 / q2 / q3 | 50550 / 50552 / 50554 | S32 | 10 var |
| s1 / s2 / s3 | 50556 / 50558 / 50560 | U32 | 10 VA |
| pf1 / pf2 / pf3 | 50562 / 50564 / 50566 | S32 | 0.001 (sign stated) |

Common energies block (U32; the vendor's PARTIAL = resettable counters):
| Point | Address | Prints as |
| --- | --- | --- |
| kwh_import_partial | 50780 | Ea+, k Wh |
| kvarh_import_partial | 50782 | Er+, varh 10^3 (= kvarh) |
| kvah_partial | 50784 | Es, k VAh |
| kwh_export_partial | 50786 | Ea-, k Wh |
| kvarh_export_partial | 50788 | Er-, varh 10^3 (= kvarh) |

THD block (U16, "% / 10" = 0.1 %):
| Point | Address |
| --- | --- |
| thd_u12 / thd_u23 / thd_u31 | 51536 / 51537 / 51538 |
| thd_v1 / thd_v2 / thd_v3 | 51539 / 51540 / 51541 |
| thd_i1 / thd_i2 / thd_i3 | 51542 / 51543 / 51544 |

## Excluded - and why

- **Non-resettable total energy counters** (51311-51314): printed unit "Wh / 1E-06" is
  ambiguous notation and they are U16 (would wrap quickly at that magnitude) - never
  guess a counter scale.
- **Second PF set + PF-type flags** (50574-50585): the sign convention is not stated on
  those rows; the signed set (50542/50562-50566) carries the same quantities with the
  stated convention.
- **Tariff energies** (50848+), **max/avg statistics** (51070+ and the per-block mirrors),
  **the hour meters**, **all 16-bit mirror blocks** ("No Affected by CT/VT", 51280+ and
  the Specific blocks at 768/1792+) - the 32-bit blocks carry the same quantities at
  full precision.
- Alarms, actions, extensions, industrialization and legacy sections - configuration and
  control, not metering.
- All setup/writable registers: the edge agent is read-only by design.

## Tested vs untested

Tested: register-evidence vitest asserts every listed address, type, scale, the three
gates blocking YAML until chosen, span overlaps, and both base choices in the YAML.
Full web suite and production build green. Picker pixel-verified on desktop and mobile.

NOT tested: real hardware. Word order, function code and address base are installer
choices by design; validate several independent readings against the meter's own display
before trusting values.
