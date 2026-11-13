# Schneider EM1X00 supplied parameter map

The two supplied table images contain 26 rows: A/A1/A2/A3,
V LL/V12/V23/V31/V LN/V1/V2/V3/F, W/W1/W2/W3,
VAR/VAR1/VAR2/VAR3, VA/VA1/VA2/VA3 and PF.
Original names/descriptions and listed decimal addresses are retained. Internal
point IDs are lowercase with underscores, such as `v_ll`; these are not changed
parameter meanings. No energy counters or PF1/PF2/PF3 were added.

Source cross-check: Schneider EAV95462 06/2014, Table 6-3 pp48-49,
https://f000.backblazeb2.com/file/poojapower/Conzerv+Energy+Meter_PPP.pdf .
FC03, Float32 (two registers), scale 1. Current, voltage and frequency are
EM1220-only per the manual. The current full-map form therefore requires
EM1220 confirmation; other/unknown models explicitly cannot use the full map.
A verified model-specific subset is future work, not guessed support.

The supplied UART image supports 9600 baud, 8 data bits, even parity and
1 stop bit. These settings are saved with the profile for later source setup.
No COM port or slave ID is inferred or saved as a template default.

The manual's ModScan32 "Swapped FP" label and listed address do not establish
this driver's byte/word layout or wire-address convention. Both remain unfilled
until confirmed from working device evidence. Synthetic test selections of
cdab and listed-minus-one are test cases only, never owner defaults.
Advanced choices are currently exposed for verification; this is not yet a
nontechnical automatic decoder selection or hardware-tested USB workflow.

Saved profiles persist framing and point metadata in tenant-scoped storage.
The source wizard saves/selects a reusable map without register re-entry, but
saving does not scan ports, send diagnostics, apply config or start collection.
Unsupported/unselected meter parameters may return zero; zero is not proof of
measurement. Complete source setup still requires an explicit connection/test,
signed source apply, confirmed reload and a recent measured reading.
