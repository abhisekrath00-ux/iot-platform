// Edge setup helpers: built-in templates and the device block of edge-agent.yaml.
// Template numbers come from docs/devices/*.md (never run on real hardware).

export interface TplPoint { id: string; register: number; unit: string; min: number; max: number; scale?: number; type?: string; }
export interface EdgeTemplate { id: string; name: string; link: 'rtu' | 'tcp'; func: number; type: string; word_order: string; points: TplPoint[]; note: string; provisional?: string;
  // wordOrderUnverified: the vendor documents do not state the multi-register word order, so the
  // template's word_order is only a suggestion - the UI must make the installer choose it explicitly
  // (from their exact model's documentation or a live display check) before any YAML is generated.
  wordOrderUnverified?: boolean;
  unitsUnverified?: boolean; // the vendor reports units dynamically (unit registers or config); the installer must confirm the meter's configured units
  // pduBaseUnverified: the vendor lists register numbers but does not state whether the on-wire
  // (PDU) address is the listed number or the listed number minus 1. The UI must make the
  // installer pick explicitly before any YAML is generated.
  pduBaseUnverified?: boolean;
  // funcUnverified: the vendor does not state the read function code (FC 03 holding vs FC 04
  // input). The UI must make the installer pick explicitly; the emitted YAML uses the choice.
  funcUnverified?: boolean;
  // stopBits: vendor-documented RTU stop bits (1 or 2). When set, the form presets this in the
  // serial settings instead of the generic 1; the emitted YAML carries the operator's value.
  stopBits?: 1 | 2;
  // serial: vendor-documented FACTORY serial settings (manual-stated only). The form shows them as a
  // hint with an explicit Apply button; the installer's edited values are never silently replaced.
  serial?: { baud?: number; parity?: 'none' | 'even' | 'odd'; address?: number }; }

const p = (id: string, register: number, unit: string, min: number, max: number): TplPoint => ({ id, register, unit, min, max });
// ps adds an explicit scale and/or type for scaled-integer maps (e.g. Socomec U32 in V 10e-2);
// omitted fields fall back to scale 1 and the template type, as before.
const ps = (id: string, register: number, unit: string, min: number, max: number, scale: number, type?: string): TplPoint => ({ id, register, unit, min, max, scale, type });

export const EDGE_TEMPLATES: EdgeTemplate[] = [
  {
    id: 'selec-mx300', name: 'Selec MX300-1-C-CE (single-phase meter)', link: 'rtu', func: 4, type: 'f32', word_order: 'abcd',
    note: 'From the manufacturer leaflet. Factory serial 9600 8N1, address 1. Power units unverified. Never run on a real meter.',
    points: [p('voltage', 0, 'V', 0, 600000), p('current', 2, 'A', 0, 10000), p('active_power', 4, 'kW', -1e6, 1e6), p('reactive_power', 6, 'kvar', -1e6, 1e6), p('apparent_power', 8, 'kVA', 0, 1e6), p('power_factor', 10, '', -1, 1), p('frequency', 12, 'Hz', 0, 65)],
  },
  {
    id: 'selec-mfm383a', name: 'Selec MFM383A-C (three-phase meter)', link: 'rtu', func: 4, type: 'f32', word_order: 'abcd',
    note: 'From the manufacturer manual. Never run on a real meter.',
    points: [p('v1n', 0, 'V', 0, 100000), p('v2n', 2, 'V', 0, 100000), p('v3n', 4, 'V', 0, 100000), p('i1', 16, 'A', 0, 100000), p('i2', 18, 'A', 0, 100000), p('i3', 20, 'A', 0, 100000), p('kw_total', 42, 'kW', -1e7, 1e7), p('pf_avg', 54, '', -1, 1), p('freq', 56, 'Hz', 0, 65), p('kwh', 58, 'kWh', 0, 1e12)],
  },
  {
    id: 'eastron-sdm630', name: 'Eastron SDM630 Modbus (three-phase meter)', link: 'rtu', func: 4, type: 'f32', word_order: 'abcd',
    note: 'From the official SDM630Modbus Protocol V1.8 PDF (docs/devices/eastron-sdm630.md). Factory serial 9600 8N1, address 1. Units as printed (W/VA/VAr). PF sign = current direction. Read-only; never run on a real meter.',
    points: [
      p('v1n', 0, 'V', 0, 1000), p('v2n', 2, 'V', 0, 1000), p('v3n', 4, 'V', 0, 1000),
      p('i1', 6, 'A', 0, 10000), p('i2', 8, 'A', 0, 10000), p('i3', 10, 'A', 0, 10000),
      p('w1', 12, 'W', -1e8, 1e8), p('w2', 14, 'W', -1e8, 1e8), p('w3', 16, 'W', -1e8, 1e8),
      p('va1', 18, 'VA', 0, 1e8), p('va2', 20, 'VA', 0, 1e8), p('va3', 22, 'VA', 0, 1e8),
      p('var1', 24, 'VAr', -1e8, 1e8), p('var2', 26, 'VAr', -1e8, 1e8), p('var3', 28, 'VAr', -1e8, 1e8),
      p('pf1', 30, '', -1, 1), p('pf2', 32, '', -1, 1), p('pf3', 34, '', -1, 1),
      p('v_avg_ln', 42, 'V', 0, 1000), p('i_avg', 46, 'A', 0, 10000), p('i_sum', 48, 'A', 0, 30000),
      p('w_total', 52, 'W', -1e8, 1e8), p('va_total', 56, 'VA', 0, 1e8), p('var_total', 60, 'VAr', -1e8, 1e8),
      p('pf_total', 62, '', -1, 1), p('freq', 70, 'Hz', 0, 70),
      p('kwh_import', 72, 'kWh', 0, 1e12), p('kwh_export', 74, 'kWh', 0, 1e12),
      p('kvarh_import', 76, 'kVArh', 0, 1e12), p('kvarh_export', 78, 'kVArh', 0, 1e12),
      p('kvah_total', 80, 'kVAh', 0, 1e12), p('ah_total', 82, 'Ah', 0, 1e12), p('w_demand', 84, 'W', -1e8, 1e8),
      p('v12', 200, 'V', 0, 1000), p('v23', 202, 'V', 0, 1000), p('v31', 204, 'V', 0, 1000), p('v_avg_ll', 206, 'V', 0, 1000),
      p('i_neutral', 224, 'A', 0, 10000),
      p('thd_v1n', 234, '%', 0, 100), p('thd_v2n', 236, '%', 0, 100), p('thd_v3n', 238, '%', 0, 100), p('thd_i1', 240, '%', 0, 100),
    ],
  },
  {
    id: 'eastron-sdm120', name: 'Eastron SDM120 Modbus (single-phase DIN-rail meter)', link: 'rtu', func: 4, type: 'f32', word_order: 'abcd',
    serial: { baud: 2400, parity: 'none', address: 1 },
    note: 'From the official SDM120Modbus Protocol V2.4 PDF (docs/devices/eastron-sdm120.md). Factory serial 2400 8N1 (NOT 9600 like the SDM630), address 1, no broadcast. Units as printed. Read-only; never run on a real meter.',
    points: [
      p('voltage', 0, 'V', 0, 1000), p('current', 6, 'A', 0, 1000),
      p('active_power', 12, 'W', -1e7, 1e7), p('apparent_power', 18, 'VA', 0, 1e7), p('reactive_power', 24, 'VAr', -1e7, 1e7),
      p('power_factor', 30, '', -1, 1), p('frequency', 70, 'Hz', 0, 70),
      p('kwh_import', 72, 'kWh', 0, 1e12), p('kwh_export', 74, 'kWh', 0, 1e12),
      p('kvarh_import', 76, 'kvarh', 0, 1e12), p('kvarh_export', 78, 'kvarh', 0, 1e12),
      p('w_demand', 84, 'W', 0, 1e7), p('max_w_demand', 86, 'W', 0, 1e7),
      p('i_demand', 258, 'A', 0, 1000), p('max_i_demand', 264, 'A', 0, 1000),
      p('kwh_total', 342, 'kWh', 0, 1e12), p('kvarh_total', 344, 'kvarh', 0, 1e12),
    ],
  },
  {
    id: 'socomec-diris-a40', name: 'Socomec DIRIS A-40, load 1 (three-phase panel meter)', link: 'rtu', func: 4, type: 'u32', word_order: 'abcd',
    wordOrderUnverified: true,
    pduBaseUnverified: true,
    funcUnverified: true,
    note: 'Every register number, hex address, size, type and scale verified line-by-line against Socomec\'s official communication table "MODBUS tables of DIRIS A-40" CMT-1.7.4B EN (docs/devices/socomec-diris-a40.md). Reads the load-1 instantaneous block (0x4800+) plus the total energy counters (0x4D83+): scaled integers, no floats - V 10e-2, mA, mHz, PF /1000, counters as printed (kWh; "varh 10e3" is kvarh; kVAh). RS485 Modbus RTU is standard on the base unit (Ethernet variants add Modbus TCP; this template presets RTU). Serial parameters are configured on the meter display, not stated in the table - match the form to the meter. Loads 2-3, per-tariff and residual counters, harmonics and PF-type flags excluded (named in the doc). Read-only; never run on a real meter.',
    provisional: 'Addresses, sizes, types and scales are verified against Socomec\'s official communication table CMT-1.7.4B. Three things neither the table nor the operating manual 545802B state: the multi-register word order, the read function code (FC 03 holding vs FC 04 input), and whether the listed address or the listed address minus 1 goes on the wire. Make all three choices yourself below, from the meter\'s integration guidance or by testing. Then validate against the real meter: compare SEVERAL independent known readings (for example frequency, one voltage and the energy import counter) with the meter\'s own display - a single plausible value is only a smoke check, it does not prove the word order, function code or base, and says nothing about the other registers. Until that display comparison is done, every value from this template is hardware-untested.',
    points: [
      ps('v_sys_ln', 18436, 'V', 0, 1000, 0.01), ps('v_sys_ll', 18438, 'V', 0, 1000, 0.01),
      ps('i_sys', 18440, 'A', 0, 100000, 0.001), ps('freq', 18442, 'Hz', 0, 70, 0.001),
      ps('v1n', 18444, 'V', 0, 1000, 0.01), ps('v2n', 18446, 'V', 0, 1000, 0.01), ps('v3n', 18448, 'V', 0, 1000, 0.01),
      ps('v12', 18452, 'V', 0, 1000, 0.01), ps('v23', 18454, 'V', 0, 1000, 0.01), ps('v31', 18456, 'V', 0, 1000, 0.01),
      ps('i1', 18458, 'A', 0, 100000, 0.001), ps('i2', 18460, 'A', 0, 100000, 0.001), ps('i3', 18462, 'A', 0, 100000, 0.001), ps('i_n', 18464, 'A', 0, 100000, 0.001),
      ps('s_nom', 18474, 'VA', 0, 1e9, 1),
      ps('p_total', 18476, 'W', -1e9, 1e9, 1, 'i32'), ps('q_total', 18478, 'var', -1e9, 1e9, 1, 'i32'), ps('s_total', 18484, 'VA', 0, 1e9, 1),
      ps('pf_total', 18486, '', -1, 1, 0.001, 'i16'),
      ps('p1', 18488, 'W', -1e9, 1e9, 1, 'i32'), ps('p2', 18490, 'W', -1e9, 1e9, 1, 'i32'), ps('p3', 18492, 'W', -1e9, 1e9, 1, 'i32'),
      ps('q1', 18494, 'var', -1e9, 1e9, 1, 'i32'), ps('q2', 18496, 'var', -1e9, 1e9, 1, 'i32'), ps('q3', 18498, 'var', -1e9, 1e9, 1, 'i32'),
      ps('s1', 18512, 'VA', 0, 1e9, 1), ps('s2', 18514, 'VA', 0, 1e9, 1), ps('s3', 18516, 'VA', 0, 1e9, 1),
      ps('pf1', 18518, '', -1, 1, 0.001, 'i16'), ps('pf2', 18519, '', -1, 1, 0.001, 'i16'), ps('pf3', 18520, '', -1, 1, 0.001, 'i16'),
      ps('kwh_import', 19843, 'kWh', 0, 4.29e9, 1), ps('kwh_export', 19846, 'kWh', 0, 4.29e9, 1),
      ps('kvarh_import', 19849, 'kvarh', 0, 4.29e9, 1), ps('kvah_total', 19855, 'kVAh', 0, 4.29e9, 1),
    ],
  },
  {
    id: 'siemens-pac3200', name: 'Siemens SENTRON PAC3200 (three-phase power monitoring, Modbus TCP)', link: 'tcp', func: 4, type: 'f32', word_order: 'abcd',
    note: 'From the official SENTRON PAC3200 manual A5E01168664B-04, table 3-6 (docs/devices/siemens-pac3200.md). Register numbers are the manual\'s offsets; PDU addressing (address = offset) is verified against Siemens application example 40614428. Instantaneous values are Float; energy counters are Double (f64, 4 registers). Read-only: the writable counter resets and 50xxx config registers are excluded. Min/max history offsets 75-211 not in this template. Never run on a real meter.',
    provisional: 'The manual does not state the word order of the 4-register Double energy counters; this template assumes most-significant-register-first (abcd). Instantaneous Float values are unaffected. Confirm against the meter (compare one known energy reading) before relying on the energy counters.',
    points: [
      p('va_n', 1, 'V', 0, 1000), p('vb_n', 3, 'V', 0, 1000), p('vc_n', 5, 'V', 0, 1000),
      p('va_b', 7, 'V', 0, 1000), p('vb_c', 9, 'V', 0, 1000), p('vc_a', 11, 'V', 0, 1000),
      p('ia', 13, 'A', 0, 100000), p('ib', 15, 'A', 0, 100000), p('ic', 17, 'A', 0, 100000),
      p('sa', 19, 'VA', 0, 1e9), p('sb', 21, 'VA', 0, 1e9), p('sc', 23, 'VA', 0, 1e9),
      p('pa', 25, 'W', -1e9, 1e9), p('pb', 27, 'W', -1e9, 1e9), p('pc', 29, 'W', -1e9, 1e9),
      p('qa', 31, 'var', -1e9, 1e9), p('qb', 33, 'var', -1e9, 1e9), p('qc', 35, 'var', -1e9, 1e9),
      p('pfa', 37, '', 0, 1), p('pfb', 39, '', 0, 1), p('pfc', 41, '', 0, 1),
      p('thd_va', 43, '%', 0, 100), p('thd_vb', 45, '%', 0, 100), p('thd_vc', 47, '%', 0, 100),
      p('thd_ia', 49, '%', 0, 100), p('thd_ib', 51, '%', 0, 100), p('thd_ic', 53, '%', 0, 100),
      p('freq', 55, 'Hz', 45, 65),
      p('avg_vpn', 57, 'V', 0, 1000), p('avg_vpp', 59, 'V', 0, 1000), p('avg_i', 61, 'A', 0, 100000),
      p('s_total', 63, 'VA', 0, 1e9), p('p_total', 65, 'W', -1e9, 1e9), p('q_total', 67, 'var', -1e9, 1e9), p('pf_total', 69, '', 0, 1),
      p('unbal_v', 71, '%', 0, 100), p('unbal_i', 73, '%', 0, 200),
      ps('work_hours', 213, 's', 0, 999999999, 1, 'u32'), ps('universal_counter', 215, '', 0, 999999999, 1, 'u32'),
      ps('energy_import_t1', 801, 'Wh', 0, 1e12, 1, 'f64'), ps('energy_import_t2', 805, 'Wh', 0, 1e12, 1, 'f64'),
      ps('energy_export_t1', 809, 'Wh', 0, 1e12, 1, 'f64'), ps('energy_export_t2', 813, 'Wh', 0, 1e12, 1, 'f64'),
      ps('reactive_import_t1', 817, 'varh', 0, 1e12, 1, 'f64'), ps('reactive_import_t2', 821, 'varh', 0, 1e12, 1, 'f64'),
      ps('reactive_export_t1', 825, 'varh', 0, 1e12, 1, 'f64'), ps('reactive_export_t2', 829, 'varh', 0, 1e12, 1, 'f64'),
      ps('apparent_t1', 833, 'VAh', 0, 1e12, 1, 'f64'), ps('apparent_t2', 837, 'VAh', 0, 1e12, 1, 'f64'),
    ],
  },
  {
    id: 'siemens-pac4200', name: 'Siemens SENTRON PAC4200 (three-phase power monitoring, Modbus TCP/RTU)', link: 'tcp', func: 4, type: 'f32', word_order: 'abcd',
    wordOrderUnverified: true,
    note: 'From the official SENTRON PAC4200 system manual A5E02316180B-03, appendix A.3 tables A-1 and A-14 (docs/devices/siemens-pac4200.md). Offsets used verbatim; addressing IS verified - the PAC RS485 module manual\'s own frame examples (A5E02091800B-01 section 12.1) put the listed offset on the wire unmodified (offset 50001 = frame C3 51, offset 60006 = frame EA 66). FC 03 and 04 are both stated for all listed variables; this template uses FC 04. All values are Float (2 registers); the Double energy table is excluded in favour of the identical Float energy counters at 2801-2819. Modbus TCP on the base unit\'s Ethernet, or RTU via the PAC RS485 module with the same offsets. Read-only; never run on a real meter.',
    provisional: 'Verified against Siemens\' own manuals: every offset, the Float format, the read function codes (03 and 04 both allowed), and the addressing (offset = on-wire address, proven by Siemens\' frame examples). The one thing neither Siemens document states is the multi-register word order - so no default is trusted: choose the word order yourself below, from your exact device\'s documentation or by comparing SEVERAL known readings (frequency, one voltage, one current) against the meter\'s own display. A single plausible reading is only a smoke check. Until that display comparison is done, every value from this template is hardware-untested.',
    points: [
      p('va_n', 1, 'V', 0, 1000), p('vb_n', 3, 'V', 0, 1000), p('vc_n', 5, 'V', 0, 1000),
      p('va_b', 7, 'V', 0, 1000), p('vb_c', 9, 'V', 0, 1000), p('vc_a', 11, 'V', 0, 1000),
      p('ia', 13, 'A', 0, 100000), p('ib', 15, 'A', 0, 100000), p('ic', 17, 'A', 0, 100000),
      p('sa', 19, 'VA', 0, 1e9), p('sb', 21, 'VA', 0, 1e9), p('sc', 23, 'VA', 0, 1e9),
      p('pa', 25, 'W', -1e9, 1e9), p('pb', 27, 'W', -1e9, 1e9), p('pc', 29, 'W', -1e9, 1e9),
      p('qa', 31, 'var', -1e9, 1e9), p('qb', 33, 'var', -1e9, 1e9), p('qc', 35, 'var', -1e9, 1e9),
      p('pfa', 37, '', 0, 1), p('pfb', 39, '', 0, 1), p('pfc', 41, '', 0, 1),
      p('thd_v_ab', 43, '%', 0, 100), p('thd_v_bc', 45, '%', 0, 100), p('thd_v_ca', 47, '%', 0, 100),
      p('freq', 55, 'Hz', 45, 65),
      p('avg_vpn', 57, 'V', 0, 1000), p('avg_vpp', 59, 'V', 0, 1000), p('avg_i', 61, 'A', 0, 100000),
      p('s_total', 63, 'VA', 0, 1e9), p('p_total', 65, 'W', -1e9, 1e9), p('q_total', 67, 'var', -1e9, 1e9), p('pf_total', 69, '', -1, 1),
      p('unbal_v', 71, '%', 0, 100), p('unbal_i', 73, '%', 0, 100),
      p('energy_import_t1', 2801, 'Wh', 0, 1e12), p('energy_import_t2', 2803, 'Wh', 0, 1e12),
      p('energy_export_t1', 2805, 'Wh', 0, 1e12), p('energy_export_t2', 2807, 'Wh', 0, 1e12),
      p('reactive_import_t1', 2809, 'varh', 0, 1e12), p('reactive_import_t2', 2811, 'varh', 0, 1e12),
      p('reactive_export_t1', 2813, 'varh', 0, 1e12), p('reactive_export_t2', 2815, 'varh', 0, 1e12),
      p('apparent_t1', 2817, 'VAh', 0, 1e12), p('apparent_t2', 2819, 'VAh', 0, 1e12),
    ],
  },
  {
    id: 'siemens-pac2200', name: 'Siemens SENTRON PAC2200 (three-phase power monitoring, Modbus TCP; RTU via optional RS485 interface)', link: 'tcp', func: 4, type: 'f32', word_order: 'abcd',
    wordOrderUnverified: true,
    note: 'Every offset, register count, format and unit verified line-by-line against Siemens\' official PAC2200 manual L1V30415167B-06 (Industry Support entry 109746835, appendix A.1 table A-3; docs/devices/siemens-pac2200.md). FC 03 and 04 are both vendor-stated for all measured variables; this template uses FC 04. Listed offsets are the on-wire start addresses per the manual\'s own access notes. Instantaneous values are Float (2 registers); the tariff-1 energy counters are Double (f64, 4 registers, Wh/varh, overflow 1.0e+12). Total PF is excluded - the manual leaves its range unstated; per-phase PF is a documented 0...1. RTU is an optional interface (4800-115200 baud, 8N2/8E1/8O1/8N1, address 1-247, no factory default stated): match the form to your device. Read-only; never run on a real meter.',
    provisional: 'Offsets, register counts, formats, units and FC 04 are verified against Siemens\' official PAC2200 manual L1V30415167B-06. One thing the manual does NOT state: the byte/word order of its multi-register values (Float 2 registers, Double energy 4 registers), and no swap setting is documented. Choose the word order yourself below, from your exact device\'s documentation or by comparing SEVERAL independent known readings (for example frequency, one voltage and one current) with the meter\'s own display - a single plausible value is only a smoke check, it does not prove the word order and says nothing about the other registers. The Double energy counters are equally affected. Until that display comparison is done, every value from this template is hardware-untested.',
    points: [
      p('v1n', 1, 'V', 0, 1000), p('v2n', 3, 'V', 0, 1000), p('v3n', 5, 'V', 0, 1000),
      p('v12', 7, 'V', 0, 1000), p('v23', 9, 'V', 0, 1000), p('v31', 11, 'V', 0, 1000),
      p('i1', 13, 'A', 0, 100000), p('i2', 15, 'A', 0, 100000), p('i3', 17, 'A', 0, 100000),
      p('s1', 19, 'VA', 0, 1e9), p('s2', 21, 'VA', 0, 1e9), p('s3', 23, 'VA', 0, 1e9),
      p('p1', 25, 'W', -1e9, 1e9), p('p2', 27, 'W', -1e9, 1e9), p('p3', 29, 'W', -1e9, 1e9),
      p('q1', 31, 'var', -1e9, 1e9), p('q2', 33, 'var', -1e9, 1e9), p('q3', 35, 'var', -1e9, 1e9),
      p('pf1', 37, '', 0, 1), p('pf2', 39, '', 0, 1), p('pf3', 41, '', 0, 1),
      p('freq', 55, 'Hz', 45, 65),
      p('v_avg_ln', 57, 'V', 0, 1000), p('v_avg_ll', 59, 'V', 0, 1000), p('i_avg', 61, 'A', 0, 100000),
      p('s_total', 63, 'VA', 0, 1e9), p('p_total', 65, 'W', -1e9, 1e9), p('q_total', 67, 'var', -1e9, 1e9), p('i_n', 71, 'A', 0, 100000),
      ps('energy_import', 801, 'Wh', 0, 1e12, 1, 'f64'), ps('energy_export', 809, 'Wh', 0, 1e12, 1, 'f64'), ps('varh_import', 817, 'varh', 0, 1e12, 1, 'f64'),
    ],
  },
  {
    id: 'janitza-umg96rm-e', name: 'Janitza UMG 96RM-E (three-phase power analyser, Modbus TCP/RTU)', link: 'tcp', func: 4, type: 'f32', word_order: 'abcd',
    stopBits: 2,
    note: 'From the official UMG 96RM-E Modbus address list and formulary (janitza.com, docs/devices/janitza-umg96rm-e.md), "Frequently required readings" table (addresses 19000-19120). Every convention is vendor-stated: the listed numbers ARE the Modbus addresses on the wire (the operating manual prints the same readings under a "Modbus address" column), the listed addresses answer big-endian and the doc says adding 32768 switches a reading to little-endian, and the slave supports both FC 03 and FC 04 (this template uses FC 04). All values are IEEE-754 Float (2 registers) and already include the configured transformer factors. Modbus TCP on the base unit\'s Ethernet, or RTU on the RS485 with the same addresses. RTU serial per the doc\'s transfer parameters: 8 data bits, no parity, 2 stop bits on the device\'s own UART (external 1 or 2) - the form presets 2 stop bits for this template. The address list applies from firmware 3.01 / hardware release 104. Read-only; never run on a real meter.',
    points: [
      p('va_n', 19000, 'V', 0, 1000), p('vb_n', 19002, 'V', 0, 1000), p('vc_n', 19004, 'V', 0, 1000),
      p('va_b', 19006, 'V', 0, 1000), p('vb_c', 19008, 'V', 0, 1000), p('vc_a', 19010, 'V', 0, 1000),
      p('ia', 19012, 'A', 0, 100000), p('ib', 19014, 'A', 0, 100000), p('ic', 19016, 'A', 0, 100000),
      p('i_n_vec', 19018, 'A', 0, 100000),
      p('pa', 19020, 'W', -1e9, 1e9), p('pb', 19022, 'W', -1e9, 1e9), p('pc', 19024, 'W', -1e9, 1e9), p('p_sum', 19026, 'W', -1e9, 1e9),
      p('sa', 19028, 'VA', 0, 1e9), p('sb', 19030, 'VA', 0, 1e9), p('sc', 19032, 'VA', 0, 1e9), p('s_sum', 19034, 'VA', 0, 1e9),
      p('qa', 19036, 'var', -1e9, 1e9), p('qb', 19038, 'var', -1e9, 1e9), p('qc', 19040, 'var', -1e9, 1e9), p('q_sum', 19042, 'var', -1e9, 1e9),
      p('cosphi_a', 19044, '', -1, 1), p('cosphi_b', 19046, '', -1, 1), p('cosphi_c', 19048, '', -1, 1),
      p('freq', 19050, 'Hz', 45, 65), p('rotation_field', 19052, '', -1, 1),
      p('energy_total', 19060, 'Wh', 0, 1e12), p('energy_consumed', 19068, 'Wh', 0, 1e12), p('energy_delivered', 19076, 'Wh', 0, 1e12),
      p('energy_apparent', 19084, 'VAh', 0, 1e12), p('energy_reactive_ind', 19100, 'varh', 0, 1e12), p('energy_reactive_cap', 19108, 'varh', 0, 1e12),
      p('thd_v_a', 19110, '%', 0, 100), p('thd_v_b', 19112, '%', 0, 100), p('thd_v_c', 19114, '%', 0, 100),
      p('thd_i_a', 19116, '%', 0, 100), p('thd_i_b', 19118, '%', 0, 100), p('thd_i_c', 19120, '%', 0, 100),
    ],
  },
  {
    id: 'circutor-cvm-c10', name: 'Circutor CVM-C10 (three-phase analyzer, Modbus RTU, unsigned values only)', link: 'rtu', func: 4, type: 'u32', word_order: 'abcd',
    note: 'From the official CVM-C10 instruction manual M001B01-03 (docs.circutor.com, docs/devices/circutor-cvm-c10.md), memory maps (tables 19/20, addresses in hex). Addressing and format ARE verified by the manual\'s own FC 04 frame example: it reads listed address 0x0000 and returns registers 0000 084D = VL1 x10 = 212.5 V, so listed addresses go on the wire unmodified and the high word comes first (ABCD). FC 03 and 04 both stated for these variables; this template uses FC 04. All included values are unsigned 32-bit over 2 registers with documented scales (V x10, mA, Hz x100, THD % x10). SIGNED quantities are EXCLUDED: the manual does not state the wire encoding of negative values, so active/reactive power, PF and cos phi are left out until confirmed on a real unit - apparent power and the separate consumed/generated energy counters are included meanwhile. Serial parameters are programmed on the device (9600/19200 baud, parity none/even/odd, 1-2 stop bits, address 0-255): match the form to your device\'s settings and keep the device at 8 data bits. A frame is limited to 20 variables (40 registers). Read-only; never run on a real meter.',
    points: [
      ps('va_n', 0x00, 'V', 0, 1000, 0.1), ps('vb_n', 0x10, 'V', 0, 1000, 0.1), ps('vc_n', 0x20, 'V', 0, 1000, 0.1),
      ps('va_b', 0x3E, 'V', 0, 1000, 0.1), ps('vb_c', 0x40, 'V', 0, 1000, 0.1), ps('vc_a', 0x42, 'V', 0, 1000, 0.1),
      ps('ia', 0x02, 'A', 0, 100000, 0.001), ps('ib', 0x12, 'A', 0, 100000, 0.001), ps('ic', 0x22, 'A', 0, 100000, 0.001), ps('i_n', 0x44, 'A', 0, 100000, 0.001),
      ps('sa', 0x0A, 'VA', 0, 1e9, 1), ps('sb', 0x1A, 'VA', 0, 1e9, 1), ps('sc', 0x2A, 'VA', 0, 1e9, 1), ps('s_total', 0x36, 'VA', 0, 1e9, 1),
      ps('freq', 0x3C, 'Hz', 0, 70, 0.01),
      ps('thd_v_a', 0x46, '%', 0, 100, 0.1), ps('thd_v_b', 0x48, '%', 0, 100, 0.1), ps('thd_v_c', 0x4A, '%', 0, 100, 0.1),
      ps('thd_i_a', 0x4C, '%', 0, 100, 0.1), ps('thd_i_b', 0x4E, '%', 0, 100, 0.1), ps('thd_i_c', 0x50, '%', 0, 100, 0.1),
      ps('energy_consumed', 0xDE, 'Wh', 0, 1e12, 1), ps('energy_generated', 0xF2, 'Wh', 0, 1e12, 1),
      ps('reactive_ind_consumed', 0xE2, 'varh', 0, 1e12, 1), ps('reactive_cap_consumed', 0xE6, 'varh', 0, 1e12, 1),
      ps('reactive_ind_generated', 0xF6, 'varh', 0, 1e12, 1), ps('reactive_cap_generated', 0xFA, 'varh', 0, 1e12, 1),
      ps('apparent_consumed', 0xEA, 'VAh', 0, 1e12, 1), ps('apparent_generated', 0xFE, 'VAh', 0, 1e12, 1),
    ],
  },
  {
    id: 'eaton-iq250-260', name: 'Eaton IQ 250/260 / PXM2000 (three-phase power meter, Modbus RTU/TCP)', link: 'rtu', func: 3, type: 'f32', word_order: 'abcd',
    note: 'From Eaton\'s official register map IB02601006E (eaton.com, published in the Power Xpert Meter 2000 series documentation set for IQ250/260 and PXM2000; the document text itself says "IQ 250/260 Meter"), docs/devices/eaton-iq250-260-pxm2000.md. All conventions are vendor-stated, not provisional: holding registers only (FC 03); the map\'s HEX column is the wire address (the decimal column is 1-based for SCADA UIs: 40000 + decimal, strip 4, subtract 1 = the hex value); FLOAT and SINT32 span 2 registers with the lower-addressed register the high-order half (ABCD), and the doc\'s worked float example (C4E1 1DB9 = -1800.929) proves negative values are well-defined. Energy counters are SINT32 read RAW (scale 1): their implied decimal point and unit scale (units/kilo/mega) follow the meter\'s programmed Energy Format setting, so know that setting before reading magnitudes. Per-phase power and PF are non-zero only for WYE hookup (doc note 2). Frequency float reads 0-65 Hz. Serial parameters are programmed on the meter (baud 9600-57600, parity none/even/odd, 1-2 stop bits): match the form to the meter\'s settings. The same map applies over Modbus TCP on units with the Ethernet option. Read-only; never run on a real meter.',
    points: [
      ps('va_n', 0x03E7, 'V', 0, 1e6, 1), ps('vb_n', 0x03E9, 'V', 0, 1e6, 1), ps('vc_n', 0x03EB, 'V', 0, 1e6, 1),
      ps('va_b', 0x03ED, 'V', 0, 1e6, 1), ps('vb_c', 0x03EF, 'V', 0, 1e6, 1), ps('vc_a', 0x03F1, 'V', 0, 1e6, 1),
      ps('ia', 0x03F3, 'A', 0, 1e6, 1), ps('ib', 0x03F5, 'A', 0, 1e6, 1), ps('ic', 0x03F7, 'A', 0, 1e6, 1), ps('i_n', 0x0403, 'A', 0, 1e6, 1),
      ps('p_total', 0x03F9, 'W', -1e9, 1e9, 1), ps('q_total', 0x03FB, 'var', -1e9, 1e9, 1), ps('s_total', 0x03FD, 'VA', -1e9, 1e9, 1),
      ps('pf_total', 0x03FF, '', -1, 1, 1), ps('freq', 0x0401, 'Hz', 0, 65, 1),
      ps('p_a', 0x0405, 'W', -1e9, 1e9, 1), ps('p_b', 0x0407, 'W', -1e9, 1e9, 1), ps('p_c', 0x0409, 'W', -1e9, 1e9, 1),
      ps('q_a', 0x040B, 'var', -1e9, 1e9, 1), ps('q_b', 0x040D, 'var', -1e9, 1e9, 1), ps('q_c', 0x040F, 'var', -1e9, 1e9, 1),
      ps('s_a', 0x0411, 'VA', -1e9, 1e9, 1), ps('s_b', 0x0413, 'VA', -1e9, 1e9, 1), ps('s_c', 0x0415, 'VA', -1e9, 1e9, 1),
      ps('pf_a', 0x0417, '', -1, 1, 1), ps('pf_b', 0x0419, '', -1, 1, 1), ps('pf_c', 0x041B, '', -1, 1, 1),
      ps('energy_received', 0x05DB, 'Wh', -1e9, 1e9, 1, 'i32'), ps('energy_delivered', 0x05DD, 'Wh', -1e9, 1e9, 1, 'i32'),
      ps('energy_net', 0x05DF, 'Wh', -1e9, 1e9, 1, 'i32'), ps('energy_total', 0x05E1, 'Wh', -1e9, 1e9, 1, 'i32'),
      ps('varh_positive', 0x05E3, 'varh', -1e9, 1e9, 1, 'i32'), ps('varh_negative', 0x05E5, 'varh', -1e9, 1e9, 1, 'i32'),
      ps('varh_net', 0x05E7, 'varh', -1e9, 1e9, 1, 'i32'), ps('varh_total', 0x05E9, 'varh', -1e9, 1e9, 1, 'i32'),
      ps('vah_total', 0x05EB, 'VAh', -1e9, 1e9, 1, 'i32'),
    ],
  },
  {
    id: 'acrel-apm800', name: 'Acrel APM800/801/810 (three-phase network power meter, Modbus RTU/TCP)', link: 'rtu', func: 3, type: 'i32', word_order: 'abcd',
    note: 'From Acrel\'s own APM series manual V2.4 (jsacrel.cn, the manufacturer) cross-checked against the Chinese original on acrel.cn - docs/devices/acrel-apm800.md. All conventions vendor-stated, not provisional: FC 03 holding registers (the only FC the TCP option supports); the table is 0-based and every worked example reads at the exact table numbers, so listed addresses go on the wire unmodified; Int32 is high-word-first ("the high bits are in the front", Val_t = first word x 65536 + second word) and floats are IEEE-754 with the sign/exponent in the first word (the Chinese 8.3.4 works 0x474B 0xAC00 = 52140 Wh) - ABCD throughout. Values are the PRIMARY side (through the programmed CT/PT ratios). Oddity the manual itself documents: primary power floats carry the scaled value (float 110000.0 = 1100 W), so power uses f32 with scale 0.01. Energies are Float Wh/varh at scale 1 (IMP/EXP/EQL/EQC at 3000-3007; the tariff block at 3050+ is excluded - the English manual\'s "IMP at 3050-3051" example is the tariff-total register, per the Chinese original\'s table). Serial is programmed on the meter (1200-38400 baud, default 9600; none+1 stop default, none+2 stop, odd, even): match the form to the meter. APM830 has a DIFFERENT layout - not covered. Read-only; never run on a real meter.',
    points: [
      ps('ia', 1100, 'A', 0, 1e6, 0.001), ps('ib', 1102, 'A', 0, 1e6, 0.001), ps('ic', 1104, 'A', 0, 1e6, 0.001), ps('i_n', 1106, 'A', 0, 1e6, 0.001),
      ps('va_n', 1120, 'V', 0, 1.2e6, 0.1), ps('vb_n', 1122, 'V', 0, 1.2e6, 0.1), ps('vc_n', 1124, 'V', 0, 1.2e6, 0.1),
      ps('va_b', 1128, 'V', 0, 1.2e6, 0.1), ps('vb_c', 1130, 'V', 0, 1.2e6, 0.1), ps('vc_a', 1132, 'V', 0, 1.2e6, 0.1),
      ps('p_a', 1150, 'W', -1e9, 1e9, 0.01, 'f32'), ps('p_b', 1152, 'W', -1e9, 1e9, 0.01, 'f32'), ps('p_c', 1154, 'W', -1e9, 1e9, 0.01, 'f32'), ps('p_total', 1156, 'W', -1e9, 1e9, 0.01, 'f32'),
      ps('q_a', 1158, 'var', -1e9, 1e9, 0.01, 'f32'), ps('q_b', 1160, 'var', -1e9, 1e9, 0.01, 'f32'), ps('q_c', 1162, 'var', -1e9, 1e9, 0.01, 'f32'), ps('q_total', 1164, 'var', -1e9, 1e9, 0.01, 'f32'),
      ps('s_a', 1166, 'VA', -1e9, 1e9, 0.01, 'f32'), ps('s_b', 1168, 'VA', -1e9, 1e9, 0.01, 'f32'), ps('s_c', 1170, 'VA', -1e9, 1e9, 0.01, 'f32'), ps('s_total', 1172, 'VA', -1e9, 1e9, 0.01, 'f32'),
      ps('freq', 1179, 'Hz', 0, 70, 0.01, 'i16'),
      ps('pf_a', 1180, '', -1, 1, 0.001, 'i16'), ps('pf_b', 1181, '', -1, 1, 0.001, 'i16'), ps('pf_c', 1182, '', -1, 1, 0.001, 'i16'), ps('pf_total', 1183, '', -1, 1, 0.001, 'i16'),
      ps('energy_imp', 3000, 'Wh', 0, 1e12, 1, 'f32'), ps('energy_exp', 3002, 'Wh', 0, 1e12, 1, 'f32'),
      ps('varh_inductive', 3004, 'varh', 0, 1e12, 1, 'f32'), ps('varh_capacitive', 3006, 'varh', 0, 1e12, 1, 'f32'),
    ],
  },
  {
    id: 'carlo-gavazzi-em340', name: 'Carlo Gavazzi EM340 (three-phase DIN-rail meter)', link: 'rtu', func: 4, type: 'i32', word_order: 'cdab',
    serial: { baud: 9600 },
    note: 'From the official EM300 Series communication protocol CP (gavazziautomation.com, docs/devices/carlo-gavazzi-em340.md). Signed 32-bit integers with documented weights (V*10, mA, W*10, PF*1000) and LSW-first word order (cdab, per the protocol doc). Registers are the doc\'s physical addresses (Modicon 300001 = physical 0). Rows the doc marks "not available" on EM340 and the ET/EM330-only rows are excluded. Read-only; never run on a real meter.',
    points: [
      ps('v1n', 0x0000, 'V', 0, 1000, 0.1), ps('v2n', 0x0002, 'V', 0, 1000, 0.1), ps('v3n', 0x0004, 'V', 0, 1000, 0.1),
      ps('v12', 0x0006, 'V', 0, 1000, 0.1), ps('v23', 0x0008, 'V', 0, 1000, 0.1), ps('v31', 0x000A, 'V', 0, 1000, 0.1),
      ps('i1', 0x000C, 'A', 0, 100000, 0.001), ps('i2', 0x000E, 'A', 0, 100000, 0.001), ps('i3', 0x0010, 'A', 0, 100000, 0.001),
      ps('p1', 0x0012, 'W', -1e9, 1e9, 0.1), ps('p2', 0x0014, 'W', -1e9, 1e9, 0.1), ps('p3', 0x0016, 'W', -1e9, 1e9, 0.1),
      ps('s1', 0x0018, 'VA', 0, 1e9, 0.1), ps('s2', 0x001A, 'VA', 0, 1e9, 0.1), ps('s3', 0x001C, 'VA', 0, 1e9, 0.1),
      ps('q1', 0x001E, 'var', -1e9, 1e9, 0.1), ps('q2', 0x0020, 'var', -1e9, 1e9, 0.1), ps('q3', 0x0022, 'var', -1e9, 1e9, 0.1),
      ps('v_sys_ln', 0x0024, 'V', 0, 1000, 0.1), ps('v_sys_ll', 0x0026, 'V', 0, 1000, 0.1),
      ps('p_sys', 0x0028, 'W', -1e9, 1e9, 0.1), ps('s_sys', 0x002A, 'VA', 0, 1e9, 0.1), ps('q_sys', 0x002C, 'var', -1e9, 1e9, 0.1),
      ps('pf1', 0x002E, '', -1, 1, 0.001, 'i16'), ps('pf2', 0x002F, '', -1, 1, 0.001, 'i16'), ps('pf3', 0x0030, '', -1, 1, 0.001, 'i16'), ps('pf_sys', 0x0031, '', -1, 1, 0.001, 'i16'),
      ps('phase_seq', 0x0032, '', 0, 1, 1, 'i16'), ps('freq', 0x0033, 'Hz', 0, 70, 0.1, 'i16'),
      ps('kwh_import', 0x0034, 'kWh', 0, 1e12, 0.1), ps('kvarh_import', 0x0036, 'kvarh', 0, 1e12, 0.1),
      ps('kw_dmd', 0x0038, 'W', 0, 1e9, 0.1), ps('kw_dmd_peak', 0x003A, 'W', 0, 1e9, 0.1),
      ps('kwh_partial', 0x003C, 'kWh', 0, 1e12, 0.1), ps('kvarh_partial', 0x003E, 'kvarh', 0, 1e12, 0.1),
      ps('kwh_l1', 0x0040, 'kWh', 0, 1e12, 0.1), ps('kwh_l2', 0x0042, 'kWh', 0, 1e12, 0.1), ps('kwh_l3', 0x0044, 'kWh', 0, 1e12, 0.1),
      ps('kwh_t1', 0x0046, 'kWh', 0, 1e12, 0.1), ps('kwh_t2', 0x0048, 'kWh', 0, 1e12, 0.1),
      ps('kwh_export', 0x004E, 'kWh', 0, 1e12, 0.1), ps('kvarh_export', 0x0050, 'kvarh', 0, 1e12, 0.1),
    ],
  },
  {
    id: 'abb-m4m', name: 'ABB M4M 20/30 (three-phase network analyzer)', link: 'rtu', func: 3, type: 'u32', word_order: 'abcd',
    wordOrderUnverified: true,
    note: 'Every address, scale and signed/unsigned type verified line-by-line against ABB\'s official workbook 9AKK107492A7627 v1.3O, M4M 20/30 all versions (docs/devices/abb-m4m.md). FC 03 (holding registers), NOT FC 04. M4M 30 Ethernet speaks Modbus TCP with the same map. UINT64 energy counters excluded (driver has no u64/i64). Read-only; never run on a real meter.',
    provisional: 'Addresses, scales and types are verified against ABB\'s official workbook 9AKK107492A7627 v1.3O. ABB\'s documents do NOT state the multi-register word order, so no default is trusted: choose the word order yourself below - from your exact model\'s documentation, or by comparing one 32-bit reading (e.g. L1 voltage) against the meter display and picking the order that matches. The mirror\'s "abcd" claim is not adopted as fact. Single-register points (frequency, power factors) are unaffected by word order. Either way, an unvalidated choice is not hardware-tested: confirm one real reading against the meter display during the pilot.',
    points: [
      ps('v_sys_ln', 23296, 'V', 0, 1000, 0.1),
      ps('v1n', 23298, 'V', 0, 1000, 0.1), ps('v2n', 23300, 'V', 0, 1000, 0.1), ps('v3n', 23302, 'V', 0, 1000, 0.1),
      ps('v12', 23304, 'V', 0, 1000, 0.1), ps('v23', 23306, 'V', 0, 1000, 0.1), ps('v31', 23308, 'V', 0, 1000, 0.1),
      ps('i_sys', 23310, 'A', 0, 100000, 0.01), ps('i1', 23312, 'A', 0, 100000, 0.01), ps('i2', 23314, 'A', 0, 100000, 0.01), ps('i3', 23316, 'A', 0, 100000, 0.01), ps('i_n', 23318, 'A', 0, 100000, 0.01),
      ps('p_total', 23322, 'W', -1e9, 1e9, 0.01, 'i32'), ps('p1', 23324, 'W', -1e9, 1e9, 0.01, 'i32'), ps('p2', 23326, 'W', -1e9, 1e9, 0.01, 'i32'), ps('p3', 23328, 'W', -1e9, 1e9, 0.01, 'i32'),
      ps('q_total', 23330, 'var', -1e9, 1e9, 0.01, 'i32'), ps('q1', 23332, 'var', -1e9, 1e9, 0.01, 'i32'), ps('q2', 23334, 'var', -1e9, 1e9, 0.01, 'i32'), ps('q3', 23336, 'var', -1e9, 1e9, 0.01, 'i32'),
      ps('s_total', 23338, 'VA', 0, 1e9, 0.01, 'i32'), ps('s1', 23340, 'VA', 0, 1e9, 0.01, 'i32'), ps('s2', 23342, 'VA', 0, 1e9, 0.01, 'i32'), ps('s3', 23344, 'VA', 0, 1e9, 0.01, 'i32'),
      ps('freq', 23346, 'Hz', 40, 70, 0.01, 'u16'),
      ps('pf_total', 23360, '', -1, 1, 0.001, 'i16'), ps('pf1', 23361, '', -1, 1, 0.001, 'i16'), ps('pf2', 23362, '', -1, 1, 0.001, 'i16'), ps('pf3', 23363, '', -1, 1, 0.001, 'i16'),
    ],
  },
  {
    id: 'abb-b23-b24', name: 'ABB B23/B24 (three-phase DIN-rail meter, Modbus RTU on RS-485)', link: 'rtu', func: 3, type: 'u32', word_order: 'abcd',
    note: 'Every address, register count, scale, signed/unsigned type and unit verified line-by-line against ABB\'s official B23/B24 User Manual 2CMC485003M0201 Revision A, chapter 9 (library.e.abb.com; docs/devices/abb-b23-b24.md). ALL conventions are vendor-stated: FC 03 holding registers; the listed hex start register "is expressed exactly as it is sent on the bus" (no 40001 offset, no minus 1); multi-register values are most-significant-byte first in the lowest register (ABCD). Powers are documented 32-bit signed with 2 decimals (max about +/-21 MW). Energy accumulators (5000+, 4 registers = 64-bit unsigned) are EXCLUDED - the driver has no u64; read energy via a pulse output or a 32-bit partial counter instead. Missing quantities read as FFFF... (unsigned) or max value (signed) per the manual, so out-of-range values mean "not available on this meter variant". Serial is set on the meter\'s RS485 menu - match the form to it. Read-only; never run on a real meter.',
    points: [
      ps('v1n', 23296, 'V', 0, 1000, 0.1), ps('v2n', 23298, 'V', 0, 1000, 0.1), ps('v3n', 23300, 'V', 0, 1000, 0.1),
      ps('v12', 23302, 'V', 0, 1000, 0.1), ps('v23', 23304, 'V', 0, 1000, 0.1), ps('v31', 23306, 'V', 0, 1000, 0.1),
      ps('i1', 23308, 'A', 0, 100000, 0.01), ps('i2', 23310, 'A', 0, 100000, 0.01), ps('i3', 23312, 'A', 0, 100000, 0.01), ps('i_n', 23314, 'A', 0, 100000, 0.01),
      ps('p_total', 23316, 'W', -2.1e7, 2.1e7, 0.01, 'i32'), ps('p1', 23318, 'W', -2.1e7, 2.1e7, 0.01, 'i32'), ps('p2', 23320, 'W', -2.1e7, 2.1e7, 0.01, 'i32'), ps('p3', 23322, 'W', -2.1e7, 2.1e7, 0.01, 'i32'),
      ps('q_total', 23324, 'var', -2.1e7, 2.1e7, 0.01, 'i32'), ps('q1', 23326, 'var', -2.1e7, 2.1e7, 0.01, 'i32'), ps('q2', 23328, 'var', -2.1e7, 2.1e7, 0.01, 'i32'), ps('q3', 23330, 'var', -2.1e7, 2.1e7, 0.01, 'i32'),
      ps('s_total', 23332, 'VA', 0, 2.1e7, 0.01, 'i32'), ps('s1', 23334, 'VA', 0, 2.1e7, 0.01, 'i32'), ps('s2', 23336, 'VA', 0, 2.1e7, 0.01, 'i32'), ps('s3', 23338, 'VA', 0, 2.1e7, 0.01, 'i32'),
      ps('freq', 23340, 'Hz', 40, 70, 0.01, 'u16'),
      ps('pf_total', 23354, '', -1, 1, 0.001, 'i16'), ps('pf1', 23355, '', -1, 1, 0.001, 'i16'), ps('pf2', 23356, '', -1, 1, 0.001, 'i16'), ps('pf3', 23357, '', -1, 1, 0.001, 'i16'),
    ],
  },
  {
    id: 'janitza-umg96rm', name: 'Janitza UMG 96RM (power analyser, firmware 1.14+)', link: 'rtu', func: 3, type: 'f32', word_order: 'abcd',
    stopBits: 2,
    funcUnverified: true,
    note: 'Every address, format, unit and meaning verified line-by-line against Janitza\'s official "UMG 96RM Modbus-address list and Formulary" (janitza-mal-umg96rm-en.pdf, firmware rel. 1.14+, janitza.com; docs/devices/janitza-umg96rm.md). The register base is vendor-stated by the UMG 96RM User Manual (janitza-bhb-umg96rm-en.pdf, Table 2), which prints the same readings under a "Modbus Address" column - listed numbers go on the wire unmodified. Points come from the curated "Frequently required readings" float block at 19000+ (pf_total from the Measured-values block at 826). Word order is vendor-stated: listed addresses return Big-Endian (ABCD); adding 32768 to an address returns Little-Endian instead - do NOT use +32768 here. Serial per the manual: 9600-115200 baud, 8 data bits, parity none, 2 stop bits (the form presets 2). PF points are fundamental-component cos phi. Energy counters are FLOAT in Wh (converted to kWh by the scale) - no 64-bit reads needed. The extensions UMG 96RM-PN/-P/-CBM share this map. Read-only; never run on a real meter.',
    provisional: 'Addresses, formats, register base and word order are verified against Janitza\'s official address list and user manual (firmware 1.14+). One thing the documents do NOT state: which read function serves these addresses - the meter supports BOTH FC 03 holding and FC 04 input, and neither document binds the address list to one of them. Choose it yourself below, from your meter\'s documentation or by testing. Then validate against the real meter: compare SEVERAL independent known readings (for example frequency, one voltage and one current) with the meter\'s own display - a single plausible value is only a smoke check, it does not prove the function code, and says nothing about the other registers. Until that display comparison is done, every value from this template is hardware-untested.',
    points: [
      p('v1n', 19000, 'V', 0, 1000), p('v2n', 19002, 'V', 0, 1000), p('v3n', 19004, 'V', 0, 1000),
      p('v12', 19006, 'V', 0, 1000), p('v23', 19008, 'V', 0, 1000), p('v31', 19010, 'V', 0, 1000),
      p('i1', 19012, 'A', 0, 100000), p('i2', 19014, 'A', 0, 100000), p('i3', 19016, 'A', 0, 100000), p('i_n', 19018, 'A', 0, 100000),
      p('p1', 19020, 'W', -1e7, 1e7), p('p2', 19022, 'W', -1e7, 1e7), p('p3', 19024, 'W', -1e7, 1e7), p('p_total', 19026, 'W', -1e7, 1e7),
      p('s1', 19028, 'VA', 0, 1e7), p('s2', 19030, 'VA', 0, 1e7), p('s3', 19032, 'VA', 0, 1e7), p('s_total', 19034, 'VA', 0, 1e7),
      p('q1', 19036, 'var', -1e7, 1e7), p('q2', 19038, 'var', -1e7, 1e7), p('q3', 19040, 'var', -1e7, 1e7), p('q_total', 19042, 'var', -1e7, 1e7),
      p('pf1', 19044, '', -1, 1), p('pf2', 19046, '', -1, 1), p('pf3', 19048, '', -1, 1),
      p('pf_total', 826, '', -1, 1),
      p('freq', 19050, 'Hz', 40, 70),
      ps('kwh_import', 19068, 'kWh', 0, 1e9, 0.001), ps('kwh_export', 19076, 'kWh', 0, 1e9, 0.001),
      ps('kvah_total', 19084, 'kVAh', 0, 1e9, 0.001), ps('kvarh_total', 19092, 'kvarh', 0, 1e9, 0.001),
    ],
  },
  {
    id: 'kamstrup-multical-x03', name: 'Kamstrup MULTICAL 403/603/803 (thermal energy meter, Modbus module)', link: 'rtu', func: 3, type: 'f32', word_order: 'abcd',
    wordOrderUnverified: true,
    pduBaseUnverified: true,
    funcUnverified: true,
    unitsUnverified: true,
    note: 'Every register number, hex address, size, type and meaning verified line-by-line against Kamstrup\'s official data sheet "Modbus RTU / Modbus/KMP TCP/IP - MULTICAL 403/603/803" FILE100004479_C_EN_03.2025 (Kamstrup\'s Sitecore content delivery tenant, provenance chain in docs/devices/kamstrup-multical-x03.md). Points use the default datagram xx-yy-300 FLOAT values. The doc prints two numberings and states their correspondence ("the Modbus register count starts at number 1, and corresponds to the memory address 0"): the listed register numbers are stored here and the base gate below decides whether register N or its hex address (N-1) goes on the wire. Units are DYNAMIC: each value\'s SI unit is set by the meter configuration and readable in unit registers 83+ (kWh=2, kW=22, m3/h=42, m3=40, degC=37) - the installer gate below confirms the meter reports the units this template labels. Registers a model lacks answer with the documented invalid float 0x4F800000, so a wildly out-of-range reading means "not present on this meter/configuration", not a fault. The HC-003-82 Modbus TCP module carries the same map; this template presets RTU (HC-003-67). Serial parameters are set on the module, not stated in this data sheet - match the form to the module. Read-only; never run on a real meter.',
    provisional: 'Addresses, sizes and types are verified against Kamstrup\'s official data sheet FILE100004479_C_EN_03.2025. Three things the data sheet does NOT state: the multi-register byte/word order, the read function code (FC 03 vs FC 04; "3 or 4" appears only in the PQT-controller section), and which numbering goes on the wire (printed register N vs hex address N-1). Make all three choices yourself below, from the module\'s documentation or by testing. ALSO confirm the meter\'s configured units: this template labels energy kWh, power kW, flow m3/h, volume m3 and temperatures degC - read the unit registers (flow 83, power 85, t1 86, t2 87, diff 90, E1 93, E3 95, V1 109; codes kWh=2, kW=22, m3/h=42, m3=40, degC=37) or check the meter display/METERTOOL, because with other configured units every value is off by the unit ratio. Then validate against the real meter: compare SEVERAL independent known readings (for example t1 temperature, flow and heat energy) with the meter\'s own display - a single plausible value is only a smoke check, it does not prove the word order, function code, base or units, and says nothing about the other registers. Until that display comparison is done, every value from this template is hardware-untested.',
    points: [
      p('flow_m3h', 1, 'm\u00b3/h', 0, 100000),
      p('power_kw', 5, 'kW', 0, 1e7),
      p('t_flow_c', 7, '\u00b0C', -30, 250), p('t_return_c', 9, '\u00b0C', -30, 250), p('t_diff_k', 15, 'K', -150, 150),
      p('heat_kwh', 21, 'kWh', 0, 1e12),
      p('cooling_kwh', 25, 'kWh', 0, 1e12),
      p('volume_m3', 53, 'm\u00b3', 0, 1e12),
    ],
  },
  {
    id: 'ime-conto-d4', name: 'IME Conto D4-Pd (Legrand CE4DF3DTMID, Modbus RTU, MID 63A direct)', link: 'rtu', func: 4, type: 'u32', word_order: 'abcd',
    serial: { baud: 19200, parity: 'even', address: 5 },
    wordOrderUnverified: true,
    note: 'Every register address, size, type, scale and unit verified line-by-line against the official communication protocol IDP000372 (assets.legrand.com, REV 1.01; docs/devices/ime-conto-d4.md) for the Modbus variant CE4DF3DTMID - the pulse (CE4DF30PMID) and MBus (CE4DF3MTMID) variants are excluded. The protocol prints both a 1-based "Register Number" and the "Register Address (Dec/Hex)"; this template stores the printed Register Address, which is the on-wire address. Read FC 04 is printed per input-register row. All values are scaled integers (no floats): currents mA, voltages mV, power 0.01 kW/kvar/kVA, PF 0.001, frequency 0.01 Hz, energy 0.01 kWh. Factory serial 19200 8E1, unit ID 5 (manual IDP000190EN_04; configurable 4800-38400 baud, none/even/odd, ID 1-255). Read-only; never run on a real meter.',
    provisional: 'Addresses, sizes, types, scales, units, FC 04 and the address base are verified against the official protocol IDP000372. One thing the protocol does NOT state: the multi-register word order (it says only that bytes are big-endian). Choose the word order yourself below, from the meter\'s integration guidance or by testing. Then validate against the real meter: compare SEVERAL independent known readings (for example frequency, one voltage and the energy import counter) with the meter\'s own display - a single plausible value is only a smoke check, it does not prove the word order and says nothing about the other registers. Signed values put the sign in the most significant bit (protocol notes 1-2); PF sign shows the quadrant and register 20550 (not mapped) tells inductive vs capacitive. Until that display comparison is done, every value from this template is hardware-untested.',
    points: [
      ps('i1', 20480, 'A', 0, 1000, 0.001), ps('i2', 20482, 'A', 0, 1000, 0.001), ps('i3', 20484, 'A', 0, 1000, 0.001),
      ps('v1n', 20509, 'V', 0, 1000, 0.001), ps('v2n', 20511, 'V', 0, 1000, 0.001), ps('v3n', 20513, 'V', 0, 1000, 0.001),
      ps('v12', 20515, 'V', 0, 1000, 0.001), ps('v23', 20517, 'V', 0, 1000, 0.001), ps('v31', 20519, 'V', 0, 1000, 0.001),
      ps('freq', 20537, 'Hz', 40, 70, 0.01, 'u16'),
      ps('p_total', 20538, 'kW', -1e6, 1e6, 0.01, 'i32'), ps('q_total', 20540, 'kvar', -1e6, 1e6, 0.01, 'i32'), ps('s_total', 20544, 'kVA', 0, 1e6, 0.01, 'i32'),
      ps('pf_total', 20548, '', -1, 1, 0.001, 'i16'),
      ps('p1', 20551, 'kW', -1e6, 1e6, 0.01, 'i32'), ps('p2', 20553, 'kW', -1e6, 1e6, 0.01, 'i32'), ps('p3', 20555, 'kW', -1e6, 1e6, 0.01, 'i32'),
      ps('kwh_import', 20592, 'kWh', 0, 1e12, 0.01), ps('kwh_export', 20594, 'kWh', 0, 1e12, 0.01),
    ],
  },
  {
    id: 'satec-pm130', name: 'SATEC PM130 PLUS (PM130P/PM130E/PM130EH, Modbus RTU)', link: 'rtu', func: 3, type: 'u32', word_order: 'cdab',
    unitsUnverified: true,
    note: 'Every register address, size, type, range and scale verified line-by-line against SATEC\'s official PM130 PLUS Modbus Communications Protocol Reference Guide BG0427 Rev. A10 (satec-global.com; docs/devices/satec-pm130.md). All three conventions are vendor-stated: FC 03 (FC 04 documented as equivalent), low-word-first 32-bit order (cdab - "the first register contains the low-order word"), and listed address = on-wire address (the 4XXXXX Modicon scheme is notation only). Reads the 1-second 32-bit register set with the meter at factory defaults: 32-bit integer format (setup register 246 = 0) and Low resolution option (1V / 1A / 1kW). Serial parameters and the Modbus RTU protocol selection are configured on the meter; no factory default is stated, so match the form to your meter. Read-only; never run on a real meter.',
    provisional: 'Addresses, types, scales, word order, FC 03 and the address base are verified against SATEC\'s official guide BG0427 Rev. A10. TWO configuration dependencies. Units follow the meter\'s resolution option: the defaults (Low resolution) give the labeled units 1V, 1A and 1kW/kvar/kVA, but the High resolution option reports 0.1V, 0.01A and 1W instead - confirm the meter reports the labeled units (unit checkbox below). And the 32-bit registers must be in the default INTEGER format (setup register 246 = 0): if the meter was switched to floating point, this template decodes garbage - check register 246 in the meter setup or PAS. Wiring note: the first three voltage registers carry phase-to-neutral voltage in 4-wire mode and phase-to-phase in 3-wire mode (the guide\'s "V1/V12"); the explicit L-L set at 14012-14016 is only valid in 4-wire mode. Then validate against the real meter: compare SEVERAL independent known readings (for example frequency, one voltage and the energy import counter) with the meter\'s own display - a single plausible value is only a smoke check. Until that display comparison is done, every value from this template is hardware-untested.',
    points: [
      ps('v1n', 13952, 'V', 0, 1000, 1), ps('v2n', 13954, 'V', 0, 1000, 1), ps('v3n', 13956, 'V', 0, 1000, 1),
      ps('i1', 13958, 'A', 0, 10000, 1), ps('i2', 13960, 'A', 0, 10000, 1), ps('i3', 13962, 'A', 0, 10000, 1),
      ps('p1', 13964, 'kW', -1e7, 1e7, 1, 'i32'), ps('p2', 13966, 'kW', -1e7, 1e7, 1, 'i32'), ps('p3', 13968, 'kW', -1e7, 1e7, 1, 'i32'),
      ps('v12', 14012, 'V', 0, 1000, 1), ps('v23', 14014, 'V', 0, 1000, 1), ps('v31', 14016, 'V', 0, 1000, 1),
      ps('p_total', 14336, 'kW', -1e7, 1e7, 1, 'i32'), ps('q_total', 14338, 'kvar', -1e7, 1e7, 1, 'i32'), ps('s_total', 14340, 'kVA', 0, 1e7, 1),
      ps('pf_total', 14342, '', -1, 1, 0.001, 'i32'),
      ps('v_avg_ln', 14356, 'V', 0, 1000, 1), ps('v_avg_ll', 14358, 'V', 0, 1000, 1), ps('i_avg', 14360, 'A', 0, 10000, 1),
      ps('freq', 14468, 'Hz', 40, 70, 0.01),
      ps('kwh_import', 14720, 'kWh', 0, 1e9, 1), ps('kwh_export', 14722, 'kWh', 0, 1e9, 1),
    ],
  },
  {
    id: 'schneider-pm53xx', name: 'Schneider PowerLogic PM53xx (PM5310/20/30/31/40/41)', link: 'rtu', func: 3, type: 'f32', word_order: 'abcd',
    wordOrderUnverified: true,
    pduBaseUnverified: true,
    funcUnverified: true,
    note: 'Every register number, data type and size verified line-by-line against Schneider\'s official PM51xx_PM53xx_PMC Register List v2011_v2021_R01.xls (linked from se.com FAQ FA234017; docs/devices/schneider-pm53xx.md). All points are FLOAT32 holding registers, present on PM5310/20/30/31/40/41 per the list. INT64 Wh counters excluded (driver has no u64/i64); the FLOAT32 kWh counter at 2700 is used instead. Read-only; never run on a real meter.',
    provisional: 'Register numbers, types and sizes are verified against Schneider\'s official register list (v2011_v2021_R01). Three things Schneider\'s documents do NOT state: the multi-register word order, the register base (listed number vs listed minus 1 on the wire), and the read function code (FC 03 holding vs FC 04 input). Make all three choices yourself below, from your exact model\'s documentation. Then validate against the real meter: compare SEVERAL independent known readings (for example frequency, one voltage and one current) with the meter\'s own display - a single plausible value is only a smoke check, it does not prove the address, word order or function code, and says nothing about the other registers. Power factor total is Schneider\'s four-quadrant encoding (-2..2), not plain -1..1 - see the doc. Until that display comparison is done, every value from this template is hardware-untested.',
    points: [
      p('i1', 3000, 'A', 0, 10000), p('i2', 3002, 'A', 0, 10000), p('i3', 3004, 'A', 0, 10000), p('i_n', 3006, 'A', 0, 10000), p('i_avg', 3010, 'A', 0, 10000),
      p('v12', 3020, 'V', 0, 1000), p('v23', 3022, 'V', 0, 1000), p('v31', 3024, 'V', 0, 1000), p('v_avg_ll', 3026, 'V', 0, 1000),
      p('v1n', 3028, 'V', 0, 1000), p('v2n', 3030, 'V', 0, 1000), p('v3n', 3032, 'V', 0, 1000), p('v_avg_ln', 3036, 'V', 0, 1000),
      p('p1', 3054, 'kW', -1e7, 1e7), p('p2', 3056, 'kW', -1e7, 1e7), p('p3', 3058, 'kW', -1e7, 1e7), p('p_total', 3060, 'kW', -1e7, 1e7),
      p('q_total', 3068, 'kvar', -1e7, 1e7), p('s_total', 3076, 'kVA', 0, 1e7),
      p('pf_total', 3084, '', -2, 2), p('freq', 3110, 'Hz', 40, 70),
      p('kwh_import', 2700, 'kWh', 0, 1e12),
    ],
  },
  {
    id: 'schneider-em6400ng', name: 'Schneider EasyLogic EM6400NG+ (RS-485 models NGRSCL1/RSCL5/RSCL2)', link: 'rtu', func: 3, type: 'f32', word_order: 'abcd',
    serial: { baud: 19200, parity: 'even' },
    wordOrderUnverified: true,
    pduBaseUnverified: true,
    funcUnverified: true,
    note: 'Every register number, data type and size verified line-by-line against Schneider\'s official Public_EM6400_PM2xxx PMC Register List v1050 (se.com FAQ FA349907 attachment, EM6400 NG column = Y; docs/devices/schneider-em6400ng.md). Same PowerLogic map family as PM53xx. All points FLOAT32 holding registers. POCL1/POCL5 models have no RS-485 and are excluded. Factory serial 19200 8E1 (user manual NHA2768503). Read-only; never run on a real meter.',
    provisional: 'Register numbers, types and sizes are verified against Schneider\'s official register list (Public_EM6400_PM2xxx PMC Register List v1050, EM6400 NG column). Three things Schneider\'s documents do NOT state: the multi-register word order, the register base (listed number vs listed minus 1 on the wire), and the read function code (FC 03 holding vs FC 04 input). Make all three choices yourself below, from your exact model\'s documentation. Then validate against the real meter: compare SEVERAL independent known readings (for example frequency, one voltage and one current) with the meter\'s own display - a single plausible value is only a smoke check, it does not prove the address, word order or function code, and says nothing about the other registers. Power factor total is Schneider\'s four-quadrant encoding (-2..2), not plain -1..1 - see the doc. Until that display comparison is done, every value from this template is hardware-untested.',
    points: [
      p('i1', 3000, 'A', 0, 10000), p('i2', 3002, 'A', 0, 10000), p('i3', 3004, 'A', 0, 10000), p('i_n', 3006, 'A', 0, 10000), p('i_avg', 3010, 'A', 0, 10000),
      p('v12', 3020, 'V', 0, 1000), p('v23', 3022, 'V', 0, 1000), p('v31', 3024, 'V', 0, 1000), p('v_avg_ll', 3026, 'V', 0, 1000),
      p('v1n', 3028, 'V', 0, 1000), p('v2n', 3030, 'V', 0, 1000), p('v3n', 3032, 'V', 0, 1000), p('v_avg_ln', 3036, 'V', 0, 1000),
      p('p1', 3054, 'kW', -1e7, 1e7), p('p2', 3056, 'kW', -1e7, 1e7), p('p3', 3058, 'kW', -1e7, 1e7), p('p_total', 3060, 'kW', -1e7, 1e7),
      p('q_total', 3068, 'kvar', -1e7, 1e7), p('s_total', 3076, 'kVA', 0, 1e7),
      p('pf_total', 3084, '', -2, 2), p('freq', 3110, 'Hz', 40, 70),
      p('kwh_import', 2700, 'kWh', 0, 1e12),
    ],
  },
  {
    id: 'schneider-pm8000', name: 'Schneider PowerLogic PM8000 series (PM8000/ION7400, Modbus RTU)', link: 'rtu', func: 3, type: 'f32', word_order: 'abcd',
    note: 'Every register number, data type, size and unit verified line-by-line against Schneider\'s official PM8000 Modbus Map workbook (PM8000_Modbus_Map.xlsx, se.com download p_Doc_Ref=PM8000_Modbus_Map; docs/devices/schneider-pm8000.md). All three conventions are vendor-stated: FC 03 (user manual 7EN02-0336, Modbus server function table), most-significant-word-first (manual: "32-bit data ... is transmitted with the most-significant word first"), and listed number = on-wire address (the high-level map defines the address space as 0-65535 and the detailed map numbers sit in it directly). Power registers report in W/VAR/VA on this meter, not kW. Serial parameters (2400-115200 baud, 8N1/8N2/8E1/8E2/8O1/8O2, unit ID 1-247) are set on the meter; no factory default is stated, so match the form to your meter. Read-only; never run on a real meter.',
    provisional: 'Register numbers, types, sizes, units, word order, FC 03 and the address base are verified against Schneider\'s official PM8000 Modbus Map and user manual 7EN02-0336. Two cautions. Power is in watts here (the map lists W/VAR/VA, not kW) - divide by 1000 if you want kW. And the map does not state the power-factor sign convention, so pf_total carries a wide -2..2 range that admits both plain and four-quadrant encodings; the meter\'s own lagging/leading registers at 3172/3174 can disambiguate if you need the sign. Validate against the real meter: compare SEVERAL independent known readings (for example frequency, one voltage and one current) with the meter\'s own display - a single plausible value is only a smoke check and says nothing about the other registers. Until that display comparison is done, every value from this template is hardware-untested.',
    points: [
      p('i1', 3000, 'A', 0, 10000), p('i2', 3002, 'A', 0, 10000), p('i3', 3004, 'A', 0, 10000), p('i_avg', 3010, 'A', 0, 10000),
      p('v12', 3020, 'V', 0, 1000), p('v23', 3022, 'V', 0, 1000), p('v31', 3024, 'V', 0, 1000), p('v_avg_ll', 3026, 'V', 0, 1000),
      p('v1n', 3028, 'V', 0, 1000), p('v2n', 3030, 'V', 0, 1000), p('v3n', 3032, 'V', 0, 1000), p('v_avg_ln', 3036, 'V', 0, 1000),
      p('p1', 3054, 'W', -1e7, 1e7), p('p2', 3056, 'W', -1e7, 1e7), p('p3', 3058, 'W', -1e7, 1e7), p('p_total', 3060, 'W', -1e7, 1e7),
      p('q_total', 3068, 'var', -1e7, 1e7), p('s_total', 3076, 'VA', 0, 1e7),
      p('pf_total', 3150, '', -2, 2), p('freq', 3110, 'Hz', 40, 70),
      p('kwh_import', 2700, 'kWh', 0, 1e12), p('kwh_export', 2702, 'kWh', 0, 1e12),
    ],
  },
  {
    id: 'schneider-iem3000', name: 'Schneider Acti9 iEM3000 series (iEM3150/3155/3250/3255/3350/3355/3455/3555, Modbus RTU)', link: 'rtu', func: 3, type: 'f32', word_order: 'abcd',
    wordOrderUnverified: true,
    note: 'Every register number, data type, size and unit verified line-by-line against Schneider\'s official iEM3000 register list DOCA0005EN-07 (se.com FAQ FA370395 attachment; docs/devices/schneider-iem3000.md). Read function and address base are vendor-stated: the doc lists only FC 03 for reads and defines Register Address as the address encoded in the Modbus frame, so listed numbers go on the wire unmodified. All points FLOAT32. Reactive/apparent power (3068/3076), energy export, tariff and Int64 energy counters are excluded (not applicable on iEM3150/3250/3350, or no i64 in the driver - see the doc). Serial is configured on the meter display (9600/19200/38400 baud, even/odd/none parity, 1 stop bit, address 1-247); no factory default is stated, so match the form to your meter. Read-only; never run on a real meter.',
    provisional: 'Register numbers, types, sizes, units, FC 03 and the address base are verified against Schneider\'s official register list DOCA0005EN-07. One thing the document does NOT state: the multi-register word order. Choose the word order yourself below, from your exact model\'s documentation or by comparing SEVERAL independent known readings (for example frequency, one voltage and one current) with the meter\'s own display - a single plausible value is only a smoke check, it does not prove the word order and says nothing about the other registers. Power factor total is Schneider\'s four-quadrant encoding (-2..2), not plain -1..1 - see the doc. Until that display comparison is done, every value from this template is hardware-untested.',
    points: [
      p('i1', 3000, 'A', 0, 10000), p('i2', 3002, 'A', 0, 10000), p('i3', 3004, 'A', 0, 10000), p('i_avg', 3010, 'A', 0, 10000),
      p('v12', 3020, 'V', 0, 1000), p('v23', 3022, 'V', 0, 1000), p('v31', 3024, 'V', 0, 1000), p('v_avg_ll', 3026, 'V', 0, 1000),
      p('v1n', 3028, 'V', 0, 1000), p('v2n', 3030, 'V', 0, 1000), p('v3n', 3032, 'V', 0, 1000), p('v_avg_ln', 3036, 'V', 0, 1000),
      p('p1', 3054, 'kW', -1e7, 1e7), p('p2', 3056, 'kW', -1e7, 1e7), p('p3', 3058, 'kW', -1e7, 1e7), p('p_total', 3060, 'kW', -1e7, 1e7),
      p('pf_total', 3084, '', -2, 2), p('freq', 3110, 'Hz', 40, 70),
      p('energy_import', 45100, 'Wh', 0, 1e12),
    ],
  },
  {
    id: 'inepro-pro380', name: 'Inepro PRO380-Mod (three-phase MID meter)', link: 'rtu', func: 3, type: 'f32', word_order: 'abcd',
    serial: { baud: 9600, parity: 'even', address: 1 },
    note: 'Every register address, function code, length, unit and data type verified line-by-line against the official PRO380 user manual V2.18-8, Appendix A3.2, linked from ineprometering.com (docs/devices/inepro-pro380.md). All conventions are vendor-stated: FC 03, Float ABCD, and listed address = on-wire address (the manual\'s own frame example). Factory serial 9600 8E1, address 1. The starred aggregate rows (Voltage* 0x5000, Current* 0x500A) are excluded - the manual never defines them. Config (0x4xxx) and tariff/reset registers excluded. Read-only; never run on a real meter.',
    points: [
      p('v1n', 20482, 'V', 0, 500), p('v2n', 20484, 'V', 0, 500), p('v3n', 20486, 'V', 0, 500), p('freq', 20488, 'Hz', 40, 70),
      p('i1', 20492, 'A', 0, 10000), p('i2', 20494, 'A', 0, 10000), p('i3', 20496, 'A', 0, 10000),
      p('p_total', 20498, 'kW', -1e7, 1e7), p('p1', 20500, 'kW', -1e7, 1e7), p('p2', 20502, 'kW', -1e7, 1e7), p('p3', 20504, 'kW', -1e7, 1e7),
      p('q_total', 20506, 'kvar', -1e7, 1e7), p('s_total', 20514, 'kVA', 0, 1e7), p('pf_total', 20522, '', -1, 1),
      p('kwh_total', 24576, 'kWh', 0, 1e12), p('kwh_import', 24588, 'kWh', 0, 1e12), p('kwh_export', 24600, 'kWh', 0, 1e12),
    ],
  },
  {
    id: 'eastron-sdm72d-m', name: 'Eastron SDM72D-M (three-phase MID DIN-rail meter, kWh and W only)', link: 'rtu', func: 4, type: 'f32', word_order: 'abcd',
    wordOrderUnverified: true,
    note: 'Every address verified line-by-line against the manufacturer\'s SDM72D-M-1 User Manual V1.4, Part 3 (eastrongroup.com; docs/devices/eastron-sdm72d-m.md). FC 04 and the on-wire addresses are vendor-stated (the table prints the Modbus Protocol Start Address Hex next to each 3X number). This model measures only active kWh and W - there are no voltage, current or frequency registers. Read-only; never run on a real meter.',
    provisional: 'Register addresses and FC 04 are verified against Eastron\'s own manual V1.4. One thing the manual does NOT pin down: it says the Hi/Lo byte order "is set automatically to normal or reverse" and cannot be configured - so word order genuinely varies per unit and no default is trusted. Choose the word order yourself below, from your unit\'s documentation or by comparing one 32-bit reading (for example total power) against the meter\'s own display. Until that display comparison is done, every value from this template is hardware-untested.',
    points: [
      p('p1', 12, 'W', -1e8, 1e8), p('p2', 14, 'W', -1e8, 1e8), p('p3', 16, 'W', -1e8, 1e8), p('p_total', 52, 'W', -1e8, 1e8),
      p('kwh_import', 72, 'kWh', 0, 1e12), p('kwh_export', 74, 'kWh', 0, 1e12), p('kwh_total', 342, 'kWh', 0, 1e12),
      p('p_import', 1280, 'W', 0, 1e8), p('p_export', 1282, 'W', 0, 1e8),
    ],
  },
  {
    id: 'accuenergy-acuvim2', name: 'Accuenergy Acuvim II (three-phase power and energy meter)', link: 'rtu', func: 3, type: 'f32', word_order: 'abcd',
    wordOrderUnverified: true,
    note: 'Every address, data type, unit and the energy scaling verified line-by-line against Accuenergy\'s official Acuvim-II-V3-Modbus-map-v1.10.xlsx (accuenergy.com/support/modbus-map; docs/devices/accuenergy-acuvim2.md). FC 03 is vendor-stated per sheet header ("03H Read"); addresses are protocol addresses used verbatim. Import energy is UINT32 with Ep=Rx/10 (scale 0.1), not float. Primary-mode relationships only. Read-only; never run on a real meter.',
    provisional: 'Addresses, FC 03, types and scaling are verified against Accuenergy\'s official workbook v1.10. One thing Accuenergy\'s documents do NOT state: the word order for FLOAT values. The manual does state the uint32 layout explicitly (high word first), a strong indication of abcd, but it is not adopted as proven for floats - choose the word order yourself below, from your model\'s documentation or by comparing one float reading (for example frequency) against the meter\'s own display. Until that display comparison is done, every value from this template is hardware-untested.',
    points: [
      p('freq', 16384, 'Hz', 40, 70),
      p('v1n', 16386, 'V', 0, 1000), p('v2n', 16388, 'V', 0, 1000), p('v3n', 16390, 'V', 0, 1000), p('v_avg_ln', 16392, 'V', 0, 1000),
      p('v12', 16394, 'V', 0, 1000), p('v23', 16396, 'V', 0, 1000), p('v31', 16398, 'V', 0, 1000), p('v_avg_ll', 16400, 'V', 0, 1000),
      p('i1', 16402, 'A', 0, 10000), p('i2', 16404, 'A', 0, 10000), p('i3', 16406, 'A', 0, 10000), p('i_avg', 16408, 'A', 0, 10000), p('i_n', 16410, 'A', 0, 10000),
      p('p1', 16412, 'kW', -1e7, 1e7), p('p2', 16414, 'kW', -1e7, 1e7), p('p3', 16416, 'kW', -1e7, 1e7), p('p_total', 16418, 'kW', -1e7, 1e7),
      p('q_total', 16426, 'kvar', -1e7, 1e7), p('s_total', 16434, 'kVA', 0, 1e7), p('pf_total', 16442, '', -1, 1),
      ps('kwh_import', 16456, 'kWh', 0, 99999999.9, 0.1, 'u32'),
    ],
  },
  {
    id: 'eastron-sdm72ct-m', name: 'Eastron SDM72CT-M (three-phase CT-operated DIN-rail meter)', link: 'rtu', func: 4, type: 'f32', word_order: 'abcd',
    wordOrderUnverified: true,
    note: 'Every address verified against Eastron\'s own 1_SDM72CT-M_Protocol_V1.1.pdf (eastroneurope.com; docs/devices/eastron-sdm72ct-m.md) - read from the rendered pages because the PDF text layer is broken, then cross-checked against the internal 3X-to-hex arithmetic and Eastron\'s SDM630 protocol PDF row-for-row. FC 04 and the wire addresses are vendor-stated. PF sign = current direction (vendor note). 30073-30079 are the resettable counters; 30343/30345 the lifetime totals. Read-only; never run on a real meter.',
    provisional: 'Register addresses and FC 04 are verified against Eastron\'s own protocol document V1.1. One thing that document does NOT state: the multi-register word order. The manufacturer\'s sibling SDM72D-M manual says the Hi/Lo byte order "is set automatically to normal or reverse" and cannot be configured - so it varies per unit and no default is trusted. Choose the word order yourself below, from your unit\'s documentation or by comparing one 32-bit reading (for example frequency or total power) against the meter\'s own display. Until that display comparison is done, every value from this template is hardware-untested.',
    points: [
      p('v1n', 0, 'V', 0, 1000), p('v2n', 2, 'V', 0, 1000), p('v3n', 4, 'V', 0, 1000),
      p('i1', 6, 'A', 0, 10000), p('i2', 8, 'A', 0, 10000), p('i3', 10, 'A', 0, 10000),
      p('p1', 12, 'W', -1e8, 1e8), p('p2', 14, 'W', -1e8, 1e8), p('p3', 16, 'W', -1e8, 1e8),
      p('s1', 18, 'VA', 0, 1e8), p('s2', 20, 'VA', 0, 1e8), p('s3', 22, 'VA', 0, 1e8),
      p('q1', 24, 'VAr', -1e8, 1e8), p('q2', 26, 'VAr', -1e8, 1e8), p('q3', 28, 'VAr', -1e8, 1e8),
      p('pf1', 30, '', -1, 1), p('pf2', 32, '', -1, 1), p('pf3', 34, '', -1, 1),
      p('v_avg_ln', 42, 'V', 0, 1000), p('i_avg', 46, 'A', 0, 10000), p('i_sum', 48, 'A', 0, 30000),
      p('p_total', 52, 'W', -1e8, 1e8), p('s_total', 56, 'VA', 0, 1e8), p('q_total', 60, 'VAr', -1e8, 1e8), p('pf_total', 62, '', -1, 1),
      p('freq', 70, 'Hz', 40, 70),
      p('kwh_import', 72, 'kWh', 0, 1e12), p('kwh_export', 74, 'kWh', 0, 1e12),
      p('kvarh_import', 76, 'kVArh', 0, 1e12), p('kvarh_export', 78, 'kVArh', 0, 1e12),
      p('v12', 200, 'V', 0, 1000), p('v23', 202, 'V', 0, 1000), p('v31', 204, 'V', 0, 1000), p('v_avg_ll', 206, 'V', 0, 1000),
      p('i_neutral', 224, 'A', 0, 10000),
      p('kwh_total', 342, 'kWh', 0, 1e12), p('kvarh_total', 344, 'kVArh', 0, 1e12),
    ],
  },
  {
    id: 'carlo-gavazzi-em24-e1', name: 'Carlo Gavazzi EM24-DIN E1 (three-phase analyzer, Modbus TCP)', link: 'tcp', func: 4, type: 'i32', word_order: 'cdab',
    note: 'Every address, data type, weight and the conventions verified line-by-line against Carlo Gavazzi\'s EM24_E1_CP protocol document (gavazziautomation.com; docs/devices/carlo-gavazzi-em24-e1.md): FC 03/04 equivalent, physical address = frame address, INT32/UINT32 word order LSW->MSW (cdab), all vendor-stated. All values are scaled integers (V*10, A*1000, W/VA/var*10, PF*1000 as INT16, Hz*10 as UINT16, kWh*10). PF: negative = lead(C), positive = lag(L). Serial EM24 variants are NOT covered - this template is the Ethernet E1 model only. Read-only; never run on a real meter.',
    points: [
      ps('v1n', 0, 'V', 0, 5000, 0.1), ps('v2n', 2, 'V', 0, 5000, 0.1), ps('v3n', 4, 'V', 0, 5000, 0.1),
      ps('v12', 6, 'V', 0, 5000, 0.1), ps('v23', 8, 'V', 0, 5000, 0.1), ps('v31', 10, 'V', 0, 5000, 0.1),
      ps('i1', 12, 'A', 0, 65000, 0.001), ps('i2', 14, 'A', 0, 65000, 0.001), ps('i3', 16, 'A', 0, 65000, 0.001),
      ps('p1', 18, 'W', -2e8, 2e8, 0.1), ps('p2', 20, 'W', -2e8, 2e8, 0.1), ps('p3', 22, 'W', -2e8, 2e8, 0.1),
      ps('s1', 24, 'VA', 0, 2e8, 0.1), ps('s2', 26, 'VA', 0, 2e8, 0.1), ps('s3', 28, 'VA', 0, 2e8, 0.1),
      ps('q1', 30, 'var', -2e8, 2e8, 0.1), ps('q2', 32, 'var', -2e8, 2e8, 0.1), ps('q3', 34, 'var', -2e8, 2e8, 0.1),
      ps('v_sum_ln', 36, 'V', 0, 15000, 0.1), ps('v_sum_ll', 38, 'V', 0, 15000, 0.1),
      ps('p_total', 40, 'W', -2e8, 2e8, 0.1), ps('s_total', 42, 'VA', 0, 2e8, 0.1), ps('q_total', 44, 'var', -2e8, 2e8, 0.1),
      ps('pf1', 46, '', -1, 1, 0.001, 'i16'), ps('pf2', 47, '', -1, 1, 0.001, 'i16'), ps('pf3', 48, '', -1, 1, 0.001, 'i16'), ps('pf_total', 49, '', -1, 1, 0.001, 'i16'),
      ps('freq', 51, 'Hz', 40, 70, 0.1, 'u16'),
      ps('kwh_import', 52, 'kWh', 0, 2e8, 0.1), ps('kvarh_import', 54, 'kvarh', 0, 2e8, 0.1),
      ps('kwh_export', 78, 'kWh', 0, 2e8, 0.1), ps('kvarh_export', 80, 'kvarh', 0, 2e8, 0.1),
    ],
  },
  {
    id: 'iskra-mc7x0', name: 'Iskra MC 7x0 (MC 740/750/760 measuring centre)', link: 'rtu', func: 4, type: 't5', word_order: 'abcd',
    stopBits: 2,
    serial: { baud: 115200, parity: 'none', address: 33 },
    note: 'Every register address and type verified line-by-line against Iskra\'s MC 7x0 User\'s Manual Ver 1.01, Appendix A (iskra.eu; docs/devices/iskra-mc7x0.md). All conventions are vendor-stated: FC 04 input registers, Modicon 3xxxx notation (on-wire = listed - 30001), MSW-first word order (the manual\'s own worked example), and the T5/T6 decade-exponent encoding (type definitions cross-checked in the MC 3x0 manual, which lists identical addresses and type codes). Values decode to base units (V, A, W, var, VA, Hz) - the meter carries the magnitude in each reading\'s exponent. Factory serial settings vendor-stated (address 33, 115200 8N2): shown as a hint with an Apply button, never silently applied. Read-only; never run on a real meter.',
    provisional: 'Addresses, types, FC 04, the address base and the word order are verified against Iskra\'s own manuals. Two cautions: the manual says its register list covers the "main" registers and points to the Iskra web site for the complete, latest table - if your firmware\'s map differs, the vendor table wins. And this template reads the 32-bit decade-exponent registers (T5/T6), which need the t5/t6 decoder in the current edge agent - older agents will reject the type. Excluded and named in the doc: power factor (T7 packs two separate sign bytes no single number can represent), energies (each counter\'s scale lives in a separate exponent register that can change at runtime), demand/max values, harmonics, and all setup registers. Until one reading (for example frequency or one voltage) is compared against the meter\'s own display, every value from this template is hardware-untested.',
    points: [
      p('v1', 106, 'V', 0, 1000), p('v2', 108, 'V', 0, 1000), p('v3', 110, 'V', 0, 1000),
      p('v_avg_ln', 112, 'V', 0, 1000), p('v12', 117, 'V', 0, 1000), p('v23', 119, 'V', 0, 1000),
      p('v31', 121, 'V', 0, 1000), p('v_avg_ll', 123, 'V', 0, 1000),
      p('i1', 125, 'A', 0, 10000), p('i2', 127, 'A', 0, 10000), p('i3', 129, 'A', 0, 10000),
      p('i_neutral', 131, 'A', 0, 10000), p('i_avg', 135, 'A', 0, 10000),
      ps('p_total', 139, 'W', -1e9, 1e9, 1, 't6'), ps('p1', 141, 'W', -1e9, 1e9, 1, 't6'),
      ps('p2', 143, 'W', -1e9, 1e9, 1, 't6'), ps('p3', 145, 'W', -1e9, 1e9, 1, 't6'),
      ps('q_total', 147, 'var', -1e9, 1e9, 1, 't6'), ps('q1', 149, 'var', -1e9, 1e9, 1, 't6'),
      ps('q2', 151, 'var', -1e9, 1e9, 1, 't6'), ps('q3', 153, 'var', -1e9, 1e9, 1, 't6'),
      p('s_total', 155, 'VA', 0, 1e9), p('s1', 157, 'VA', 0, 1e9), p('s2', 159, 'VA', 0, 1e9), p('s3', 161, 'VA', 0, 1e9),
      p('freq', 104, 'Hz', 40, 70),
      ps('thd_u1', 181, '%', 0, 100, 0.01, 'u16'), ps('thd_u2', 182, '%', 0, 100, 0.01, 'u16'), ps('thd_u3', 183, '%', 0, 100, 0.01, 'u16'),
      ps('thd_i1', 187, '%', 0, 100, 0.01, 'u16'), ps('thd_i2', 188, '%', 0, 100, 0.01, 'u16'), ps('thd_i3', 189, '%', 0, 100, 0.01, 'u16'),
    ],
  },
  {
    id: 'iskra-mc3x0', name: 'Iskra MC 3x0 (MC 320/330/350 measuring centre)', link: 'rtu', func: 4, type: 't5', word_order: 'abcd',
    stopBits: 2,
    serial: { baud: 115200, parity: 'none', address: 33 },
    note: 'Every register address and type verified line-by-line against Iskra\'s MC 3x0 User\'s Manual Ver 8.00, Appendix A (iskra.eu; docs/devices/iskra-mc3x0.md), in the meter\'s MC7x0-compatible register map. Conventions vendor-stated: FC 04 input registers, Modicon 3xxxx notation (on-wire = listed - 30001), MSW-first word order (the manual\'s own worked examples), T5/T6 decade-exponent encoding. Factory serial settings vendor-stated (address 33, 115200 8N2): hint with Apply button, never silently applied. Read-only; never run on a real meter.',
    provisional: 'Addresses, types, FC 04, the address base and the word order are verified against Iskra\'s own manual. ONE configuration check first: the MC 3x0 has TWO register maps selected by holding register 40100 (0 = MC7x0-compatible, 1 = MI71x0-compatible) and the manual does not state the factory default - this template uses the MC7x0-compatible map, so confirm register 40100 reads 0 (or set it) before trusting any value. Two totals are deliberately absent: the manual\'s type codes for total reactive and total apparent power conflict with the family type definitions (T5/T7 where T6/T5 are expected), so they are excluded rather than guessed - named in the doc. Also excluded: power factor (T7 two-sign-byte format), energies (runtime exponent registers), demands and angles. Until one reading (for example frequency or one voltage) is compared against the meter\'s own display, every value from this template is hardware-untested.',
    points: [
      p('v1', 106, 'V', 0, 1000), p('v2', 108, 'V', 0, 1000), p('v3', 110, 'V', 0, 1000),
      p('v_avg_ln', 112, 'V', 0, 1000), p('v12', 117, 'V', 0, 1000), p('v23', 119, 'V', 0, 1000),
      p('v31', 121, 'V', 0, 1000), p('v_avg_ll', 123, 'V', 0, 1000),
      p('i1', 125, 'A', 0, 10000), p('i2', 127, 'A', 0, 10000), p('i3', 129, 'A', 0, 10000),
      p('i_neutral', 131, 'A', 0, 10000), p('i_total', 137, 'A', 0, 30000),
      ps('p1', 141, 'W', -1e9, 1e9, 1, 't6'), ps('p2', 143, 'W', -1e9, 1e9, 1, 't6'),
      ps('p3', 145, 'W', -1e9, 1e9, 1, 't6'), ps('p_total', 139, 'W', -1e9, 1e9, 1, 't6'),
      ps('q1', 149, 'var', -1e9, 1e9, 1, 't6'), ps('q2', 151, 'var', -1e9, 1e9, 1, 't6'), ps('q3', 153, 'var', -1e9, 1e9, 1, 't6'),
      p('s1', 157, 'VA', 0, 1e9), p('s2', 159, 'VA', 0, 1e9), p('s3', 161, 'VA', 0, 1e9),
      p('freq', 104, 'Hz', 40, 70),
      ps('thd_u1', 181, '%', 0, 100, 1, 'u16'), ps('thd_u2', 182, '%', 0, 100, 1, 'u16'), ps('thd_u3', 183, '%', 0, 100, 1, 'u16'),
      ps('thd_i1', 187, '%', 0, 100, 1, 'u16'), ps('thd_i2', 188, '%', 0, 100, 1, 'u16'), ps('thd_i3', 189, '%', 0, 100, 1, 'u16'),
    ],
  },
  {
    id: 'entes-mpr53cs', name: 'Entes MPR-53CS (three-phase network analyzer)', link: 'rtu', func: 3, type: 'u32', word_order: 'abcd',
    stopBits: 2,
    wordOrderUnverified: true,
    serial: { baud: 9600, parity: 'none', address: 1 },
    note: 'Every register address, multiplier and format verified line-by-line against Entes\' official MPR-53CS MODBUS Register Map (entes.com.tr) and user manual A4396 R3 (enteselektronik.com; docs/devices/entes-mpr53cs.md). Vendor-stated: FC 03 holding registers (the manual\'s worked read example), listed address = on-wire address (the example reads phase-1 voltage at 0x0000), 2 registers per value with printed multipliers, and factory serial settings (address 1, 9600 baud, no parity, 8 data bits, 2 stop bits): shown as a hint with an Apply button, never silently applied. Values are primary-side - the meter applies its programmed CT/VT ratios (factory 1:1), which the register map\'s xCT/xUT range notation documents. Read-only; never run on a real meter.',
    provisional: 'Addresses, formats, multipliers, FC 03, the address base and the serial defaults are verified against Entes\' own documents. TWO things to know: the documents never state the 32-bit WORD ORDER, so pick it yourself below from your unit\'s behavior or a known reading (for example frequency or one voltage) against the meter\'s display - until that comparison is done, every value from this template is hardware-untested. And readings are primary-side: if CT/VT ratios are programmed in the meter, values come out already scaled by them (factory 1:1). Excluded and named in the doc: energy counters (the map prints a 64-bit range at 2-register spacing - contradictory, so not guessed), min/max/demand registers, average cos-phi pair, angles, digital I/O, pulse and hour counters, and all setup registers.',
    points: [
      p('v1', 0, 'V', 0, 3000), p('v2', 2, 'V', 0, 3000), p('v3', 4, 'V', 0, 3000),
      ps('i1', 6, 'A', 0, 6000, 0.001), ps('i2', 8, 'A', 0, 6000, 0.001), ps('i3', 10, 'A', 0, 6000, 0.001), ps('i_neutral', 12, 'A', 0, 6000, 0.001),
      p('v12', 14, 'V', 0, 5000), p('v23', 16, 'V', 0, 5000), p('v31', 18, 'V', 0, 5000),
      ps('p1', 20, 'W', -18000, 18000, 0.1, 'i32'), ps('p2', 22, 'W', -18000, 18000, 0.1, 'i32'), ps('p3', 24, 'W', -18000, 18000, 0.1, 'i32'),
      ps('q1', 26, 'var', -18000, 18000, 0.1, 'i32'), ps('q2', 28, 'var', -18000, 18000, 0.1, 'i32'), ps('q3', 30, 'var', -18000, 18000, 0.1, 'i32'),
      ps('s1', 32, 'VA', 0, 18000, 0.1), ps('s2', 34, 'VA', 0, 18000, 0.1), ps('s3', 36, 'VA', 0, 18000, 0.1),
      ps('pf1', 38, '', -1, 1, 0.001, 'i32'), ps('pf2', 40, '', -1, 1, 0.001, 'i32'), ps('pf3', 42, '', -1, 1, 0.001, 'i32'),
      ps('p_total_import', 44, 'W', 0, 54000, 0.1, 'i32'), ps('p_total_export', 46, 'W', 0, 54000, 0.1, 'i32'),
      ps('q_total_ind', 48, 'var', 0, 54000, 0.1, 'i32'), ps('q_total_cap', 50, 'var', 0, 54000, 0.1, 'i32'),
      ps('s_total', 52, 'VA', 0, 54000, 0.1),
      ps('freq', 58, 'Hz', 40, 70, 0.01),
      ps('thd_u1', 72, '%', 0, 99.9, 0.1), ps('thd_u2', 74, '%', 0, 99.9, 0.1), ps('thd_u3', 76, '%', 0, 99.9, 0.1),
      ps('thd_i1', 78, '%', 0, 99.9, 0.1), ps('thd_i2', 80, '%', 0, 99.9, 0.1), ps('thd_i3', 82, '%', 0, 99.9, 0.1),
    ],
  },
  {
    id: 'cet-pmc53m', name: 'CET PMC-53M (three-phase power meter)', link: 'rtu', func: 3, type: 'u32', word_order: 'abcd',
    wordOrderUnverified: true,
    serial: { baud: 9600, parity: 'even', address: 100 },
    note: 'Every register address, format and scale verified line-by-line against CET\'s official PMC-53 Series User Manual V5.2A, chapter 5 register map, Protocol Version 6.0 (cet-global.com; docs/devices/cet-pmc53m.md). Vendor-stated: FC 03 holding registers, listed address = on-wire address (the manual says the 4 prefix is notation only, no subtraction), the scale convention with a worked example (raw 22003 = 220.03 V), and factory serial settings (unit ID 100, 9600 baud, 8E1): shown as a hint with an Apply button, never silently applied. Power readings are kW/kvar/kVA. Read-only; never run on a real meter.',
    provisional: 'Addresses, formats, scales, FC 03, the address base and the serial defaults are verified against CET\'s own manual. ONE thing to know: the manual never states the 32-bit WORD ORDER for the measurement registers (its demand-log table does put the high-order word at the lower address, which is why ABCD is preselected), so confirm it yourself below from a known reading (for example frequency or one voltage) against the meter\'s display - until then every value from this template is hardware-untested. Also: in Delta wiring mode the L-N voltage, per-phase power and PF registers are reserved (manual note); I4 needs firmware V1.20.01+; energy counters roll over at 99,999,999.9 kXh. Excluded and named in the doc: demands and peak logs, angles, residual current and temperature, unbalance, dPF, alarm/DO/DI bitmaps, harmonics and all setup registers.',
    points: [
      ps('v1', 40000, 'V', 0, 1000, 0.01), ps('v2', 40002, 'V', 0, 1000, 0.01), ps('v3', 40004, 'V', 0, 1000, 0.01), ps('v_avg_ln', 40006, 'V', 0, 1000, 0.01),
      ps('v12', 40008, 'V', 0, 2000, 0.01), ps('v23', 40010, 'V', 0, 2000, 0.01), ps('v31', 40012, 'V', 0, 2000, 0.01), ps('v_avg_ll', 40014, 'V', 0, 2000, 0.01),
      ps('i1', 40016, 'A', 0, 6000, 0.001), ps('i2', 40018, 'A', 0, 6000, 0.001), ps('i3', 40020, 'A', 0, 6000, 0.001), ps('i_avg', 40022, 'A', 0, 6000, 0.001),
      ps('p1', 40024, 'kW', -6000, 6000, 0.001, 'i32'), ps('p2', 40026, 'kW', -6000, 6000, 0.001, 'i32'), ps('p3', 40028, 'kW', -6000, 6000, 0.001, 'i32'), ps('p_total', 40030, 'kW', -18000, 18000, 0.001, 'i32'),
      ps('q1', 40032, 'kvar', -6000, 6000, 0.001, 'i32'), ps('q2', 40034, 'kvar', -6000, 6000, 0.001, 'i32'), ps('q3', 40036, 'kvar', -6000, 6000, 0.001, 'i32'), ps('q_total', 40038, 'kvar', -18000, 18000, 0.001, 'i32'),
      ps('s1', 40040, 'kVA', 0, 6000, 0.001, 'i32'), ps('s2', 40042, 'kVA', 0, 6000, 0.001, 'i32'), ps('s3', 40044, 'kVA', 0, 6000, 0.001, 'i32'), ps('s_total', 40046, 'kVA', 0, 18000, 0.001, 'i32'),
      ps('pf1', 40048, '', -1, 1, 0.001, 'i16'), ps('pf2', 40049, '', -1, 1, 0.001, 'i16'), ps('pf3', 40050, '', -1, 1, 0.001, 'i16'), ps('pf_total', 40051, '', -1, 1, 0.001, 'i16'),
      ps('freq', 40052, 'Hz', 40, 70, 0.01, 'u16'),
      ps('i_neutral', 40053, 'A', 0, 6000, 0.001),
      ps('kwh_import', 40100, 'kWh', 0, 99999999.9, 0.1), ps('kwh_export', 40102, 'kWh', 0, 99999999.9, 0.1),
      ps('kvarh_import', 40106, 'kvarh', 0, 99999999.9, 0.1), ps('kvarh_export', 40108, 'kvarh', 0, 99999999.9, 0.1),
    ],
  },
  {
    id: 'lovato-dmg600', name: 'Lovato DMG600/DMG610 (three-phase DIN multimeter)', link: 'rtu', func: 4, type: 'u32', word_order: 'abcd',
    serial: { baud: 9600, parity: 'none', address: 1 },
    note: 'Every register address, format and scale verified line-by-line against Lovato\'s official DMG series MODBUS Communication Protocol I317IGB02_22 (datasheet.lovatoelectric.com) plus the DMG600/610 instruction manual I411IGB02_22 for the serial defaults (docs/devices/lovato-dmg600.md). Vendor-stated: FC 04 input registers (the protocol\'s worked read example; FC 03 documented equivalent), on-wire address = listed address minus 1 (explicitly stated and shown in the example), MSW-first word order (the example response 00 01 FB 00 = 129792 = 1.29792 kW), 2 registers per value, and factory serial settings (node 1, 9600 baud, 8N1, Modbus-RTU) from the P07 menu table: shown as a hint with an Apply button, never silently applied. Read-only; never run on a real meter.',
    provisional: 'Addresses, formats, scales, FC 04, the minus-1 address base, the word order and the serial defaults are all verified against Lovato\'s own documents - unusually complete. Hardware is still untested: confirm a known reading (for example frequency or one voltage) against the meter\'s display before trusting values. ONE scaling caveat from the protocol: power readings are normally 1/100 W, but if the maximum theoretical power per phase (from the CT/VT combination) exceeds 5 MW the meter switches ALL power readings to 1/10 W - the protocol documents a query (2F70H) to check which scale is active. Excluded and named in the doc: energy counters (the 32-bit rows are not mapped for this model family and the 64-bit rows exceed the driver\'s types), asymmetry and CosPhi rows, MAX/MIN/AVG/demand blocks, harmonics, date/time and all setup registers.',
    points: [
      ps('v1', 1, 'V', 0, 1000, 0.01), ps('v2', 3, 'V', 0, 1000, 0.01), ps('v3', 5, 'V', 0, 1000, 0.01),
      ps('i1', 7, 'A', 0, 6000, 0.0001), ps('i2', 9, 'A', 0, 6000, 0.0001), ps('i3', 11, 'A', 0, 6000, 0.0001),
      ps('v12', 13, 'V', 0, 2000, 0.01), ps('v23', 15, 'V', 0, 2000, 0.01), ps('v31', 17, 'V', 0, 2000, 0.01),
      ps('p1', 19, 'W', -5000000, 5000000, 0.01, 'i32'), ps('p2', 21, 'W', -5000000, 5000000, 0.01, 'i32'), ps('p3', 23, 'W', -5000000, 5000000, 0.01, 'i32'),
      ps('q1', 25, 'var', -5000000, 5000000, 0.01, 'i32'), ps('q2', 27, 'var', -5000000, 5000000, 0.01, 'i32'), ps('q3', 29, 'var', -5000000, 5000000, 0.01, 'i32'),
      ps('s1', 31, 'VA', 0, 5000000, 0.01), ps('s2', 33, 'VA', 0, 5000000, 0.01), ps('s3', 35, 'VA', 0, 5000000, 0.01),
      ps('pf1', 37, '', -1, 1, 0.0001, 'i32'), ps('pf2', 39, '', -1, 1, 0.0001, 'i32'), ps('pf3', 41, '', -1, 1, 0.0001, 'i32'),
      ps('freq', 49, 'Hz', 40, 70, 0.001),
      ps('v_avg_ln', 51, 'V', 0, 1000, 0.01), ps('v_avg_ll', 53, 'V', 0, 2000, 0.01), ps('i_avg', 55, 'A', 0, 6000, 0.0001),
      ps('p_total', 57, 'W', -15000000, 15000000, 0.01, 'i32'), ps('q_total', 59, 'var', -15000000, 15000000, 0.01, 'i32'), ps('s_total', 61, 'VA', 0, 15000000, 0.01),
      ps('pf_total', 63, '', -1, 1, 0.0001, 'i32'),
      ps('i_neutral', 71, 'A', 0, 6000, 0.0001),
    ],
  },
  {
    id: 'eig-shark-200', name: 'Electro Industries Shark 200 (three-phase power meter, Modbus RTU)', link: 'rtu', func: 3, type: 'f32', word_order: 'abcd',
    serial: { baud: 57600, address: 1 },
    note: 'Every register address, format and scale verified line-by-line against Electro Industries/GaugeTech\'s official Shark 200 Modbus map, Appendix B of user manual E149701 (electroind.com), plus quickstart E149703 for the factory serial defaults (docs/devices/eig-shark-200.md). Vendor-stated: FC 03 holding registers (Appendix B notes), on-wire address = listed decimal address minus 1 (B.6 works the 40001-style SCADA addressing through explicitly), MSW-first 32-bit formats (B.3: the lower-addressed register is the high-order half for FLOAT/SINT32/UINT32), FLOAT = IEEE 754, and factory serial address 1 / 57600 baud / Modbus RTU from the quickstart\'s Factory Initial Default Settings: shown as a hint with an Apply button, never silently applied. Read-only; never run on a real meter.',
    provisional: 'Addresses, formats, FC 03, the minus-1 address base, the word order and the serial defaults are all verified against the vendor\'s own documents - unusually complete. Hardware is still untested: confirm a known reading (for example frequency or one voltage) against the meter\'s display before trusting values. Per-phase power and PF read zero on non-WYE hookups (vendor-stated). Parity/data bits are not stated in the quickstart - match the meter. Excluded and named in the doc: ALL energy counters (SINT32 "per energy format" - both the implied decimal point and the units/kilo/mega multiplier follow the meter\'s programmable energy-format setting, so no honest fixed scale exists), the integer Readings Block 279-305 (scale follows programmable settings; the FLOAT block carries the same quantities), secondary readings, demand averages, phase angles, min/max blocks, harmonics, wave scope, logs and all setup registers.',
    points: [
      ps('v1', 999, 'V', 0, 1000000, 1), ps('v2', 1001, 'V', 0, 1000000, 1), ps('v3', 1003, 'V', 0, 1000000, 1),
      ps('v12', 1005, 'V', 0, 1000000, 1), ps('v23', 1007, 'V', 0, 1000000, 1), ps('v31', 1009, 'V', 0, 1000000, 1),
      ps('i1', 1011, 'A', 0, 100000, 1), ps('i2', 1013, 'A', 0, 100000, 1), ps('i3', 1015, 'A', 0, 100000, 1),
      ps('p_total', 1017, 'W', -1e10, 1e10, 1), ps('q_total', 1019, 'var', -1e10, 1e10, 1), ps('s_total', 1021, 'VA', 0, 1e10, 1),
      ps('pf_total', 1023, '', -1, 1, 1),
      ps('freq', 1025, 'Hz', 0, 65, 1),
      ps('i_neutral', 1027, 'A', 0, 100000, 1),
      ps('p1', 1029, 'W', -1e10, 1e10, 1), ps('p2', 1031, 'W', -1e10, 1e10, 1), ps('p3', 1033, 'W', -1e10, 1e10, 1),
      ps('q1', 1035, 'var', -1e10, 1e10, 1), ps('q2', 1037, 'var', -1e10, 1e10, 1), ps('q3', 1039, 'var', -1e10, 1e10, 1),
      ps('s1', 1041, 'VA', 0, 1e10, 1), ps('s2', 1043, 'VA', 0, 1e10, 1), ps('s3', 1045, 'VA', 0, 1e10, 1),
      ps('pf1', 1047, '', -1, 1, 1), ps('pf2', 1049, '', -1, 1, 1), ps('pf3', 1051, '', -1, 1, 1),
      ps('i_unbalance', 1064, '%', 0, 200, 0.01, 'u16'),
      ps('thd_v1', 5999, '%', 0, 100, 0.01, 'u16'), ps('thd_v2', 6000, '%', 0, 100, 0.01, 'u16'), ps('thd_v3', 6001, '%', 0, 100, 0.01, 'u16'),
      ps('thd_i1', 6002, '%', 0, 100, 0.01, 'u16'), ps('thd_i2', 6003, '%', 0, 100, 0.01, 'u16'), ps('thd_i3', 6004, '%', 0, 100, 0.01, 'u16'),
    ],
  },
  {
    id: 'chint-dtsu666', name: 'CHINT DTSU666/DSSU666 (three-phase DIN-rail meter)', link: 'rtu', func: 3, type: 'f32', word_order: 'abcd',
    pduBaseUnverified: true,
    serial: { baud: 9600, parity: 'none', address: 1 },
    note: 'Every register address, format and scale verified line-by-line against Table 11 "ModBus protocol address table" of CHINT\'s official DTSU666 DSSU666 User Manual ZTW0.464.0104 V1 (chintglobal.com), plus its communication-settings section for the factory serial defaults (docs/devices/chint-dtsu666.md). Vendor-stated: FC 03 read / FC 10 write, IEEE754 floats in ABCD order (Table 11 Note 1), fixed unit multipliers (V x0.1, A x0.001, W/var x0.1, PF x0.001, Hz x0.01; energies already kWh), and factory serial 9600bps / N.8.1 / address 1: shown as a hint with an Apply button, never silently applied. NOT stated by the vendor: the register base (listed address vs listed minus 1) - the base gate below makes the installer choose explicitly. The DTSU666_G variant uses a DIFFERENT map (chint.eu doc, instantaneous at 0200H); do not mix. Read-only; never run on a real meter.',
    provisional: 'Addresses, formats, scales, FC 03, the ABCD float order and the serial defaults are all verified against CHINT\'s official user manual ZTW0.464.0104 V1. One thing the manual does NOT state: whether the on-wire address is the listed hex address or the listed address minus 1. Choose it yourself below, from your unit\'s documentation or a live read you can verify against the meter\'s own display. Hardware is still untested: confirm a known reading (for example frequency or one voltage) against the meter\'s display before trusting values. Vendor-stated caveats: phase voltages and per-phase power/PF rows are invalid on three-phase three-wire hookups; PF positive = inductive, negative = capacitive. Excluded and named in the doc: apparent power/energy (not in the vendor map at all), reactive energies (only on the _G variant map), all writable keyboard/setup registers including the energy-reset, primary-side data (Note 2 requires a non-public protocol doc), demand, THD and tariffs.',
    points: [
      ps('kwh_import', 0x101E, 'kWh', 0, 1e12, 1), ps('kwh_import_a', 0x1020, 'kWh', 0, 1e12, 1), ps('kwh_import_b', 0x1022, 'kWh', 0, 1e12, 1), ps('kwh_import_c', 0x1024, 'kWh', 0, 1e12, 1), ps('kwh_import_net', 0x1026, 'kWh', 0, 1e12, 1),
      ps('kwh_export', 0x1028, 'kWh', 0, 1e12, 1), ps('kwh_export_a', 0x102A, 'kWh', 0, 1e12, 1), ps('kwh_export_b', 0x102C, 'kWh', 0, 1e12, 1), ps('kwh_export_c', 0x102E, 'kWh', 0, 1e12, 1), ps('kwh_export_net', 0x1030, 'kWh', 0, 1e12, 1),
      ps('v12', 0x2000, 'V', 0, 10000, 0.1), ps('v23', 0x2002, 'V', 0, 10000, 0.1), ps('v31', 0x2004, 'V', 0, 10000, 0.1),
      ps('v1', 0x2006, 'V', 0, 10000, 0.1), ps('v2', 0x2008, 'V', 0, 10000, 0.1), ps('v3', 0x200A, 'V', 0, 10000, 0.1),
      ps('i1', 0x200C, 'A', 0, 10000, 0.001), ps('i2', 0x200E, 'A', 0, 10000, 0.001), ps('i3', 0x2010, 'A', 0, 10000, 0.001),
      ps('p_total', 0x2012, 'W', -1e9, 1e9, 0.1), ps('p1', 0x2014, 'W', -1e9, 1e9, 0.1), ps('p2', 0x2016, 'W', -1e9, 1e9, 0.1), ps('p3', 0x2018, 'W', -1e9, 1e9, 0.1),
      ps('q_total', 0x201A, 'var', -1e9, 1e9, 0.1), ps('q1', 0x201C, 'var', -1e9, 1e9, 0.1), ps('q2', 0x201E, 'var', -1e9, 1e9, 0.1), ps('q3', 0x2020, 'var', -1e9, 1e9, 0.1),
      ps('pf_total', 0x202A, '', -1, 1, 0.001), ps('pf1', 0x202C, '', -1, 1, 0.001), ps('pf2', 0x202E, '', -1, 1, 0.001), ps('pf3', 0x2030, '', -1, 1, 0.001),
      ps('freq', 0x2044, 'Hz', 0, 65, 0.01),
    ],
  },
];



export interface DeviceInput {
  deviceId: string; templateId: string; link: 'rtu' | 'tcp';
  port?: string; baud?: number; parity?: 'none' | 'even' | 'odd'; stopBits?: 1 | 2; host?: string; tcpPort?: number; address: number; intervalSec: number; provisionalConfirmed?: boolean; unitsConfirmed?: boolean; wordOrder?: string; pduBase?: 'listed' | 'minus1'; readFunc?: 3 | 4;
}

export function validateDevice(d: DeviceInput): string[] {
  const e: string[] = [];
  if (!/^[a-z0-9][a-z0-9_-]{0,39}$/.test(d.deviceId)) e.push('Device id: lowercase letters, digits, - and _ only (max 40).');
  const tpl = EDGE_TEMPLATES.find((t) => t.id === d.templateId);
  if (!tpl) e.push('Pick a template.');
  if (tpl?.provisional && !d.provisionalConfirmed) e.push('Provisional template: tick the confirmation in the safety block above once you have checked the points it lists.');
  if (tpl?.unitsUnverified && !d.unitsConfirmed) e.push('Configured units: confirm the meter reports the labeled units (its unit registers, display or configuration tool) - a correct-looking value in the wrong unit misleads silently.');
  if (tpl?.wordOrderUnverified && !d.wordOrder) e.push('Word order: the vendor does not document it - choose it explicitly from your model\'s documentation or a display check against the meter.');
  if (tpl?.pduBaseUnverified && !d.pduBase) e.push('Register base: the vendor numbers registers from 1 but does not document whether the on-wire address is the listed number or the listed number minus 1 - choose it explicitly from your model\'s documentation or a live read of a value you can verify against the meter\'s own display.');
  if (tpl?.funcUnverified && !d.readFunc) e.push('Read function: the vendor does not document whether these registers are read with FC 03 (holding) or FC 04 (input) - choose it explicitly from your model\'s documentation or a gateway/scan tool that shows which one the meter answers.');
  if (!Number.isInteger(d.address) || d.address < 1 || d.address > 247) e.push('Modbus address must be 1-247.');
  if (!(d.intervalSec >= 1 && d.intervalSec <= 3600)) e.push('Read interval must be 1-3600 seconds.');
  if (d.link === 'rtu') {
    if (!d.port || !/^(COM\d{1,3}|\/dev\/[A-Za-z0-9._\/-]+)$/.test(d.port)) e.push('Serial port looks wrong (COM3 or /dev/ttyUSB0).');
    if (![1200, 2400, 4800, 9600, 19200, 38400, 57600, 115200].includes(d.baud ?? 0)) e.push('Pick a standard baud rate.');
    if (d.stopBits !== undefined && d.stopBits !== 1 && d.stopBits !== 2) e.push('Stop bits must be 1 or 2.');
  } else {
    if (!d.host || !/^[A-Za-z0-9.-]+$/.test(d.host)) e.push('Host or IP looks wrong.');
    if (!d.tcpPort || d.tcpPort < 1 || d.tcpPort > 65535) e.push('TCP port must be 1-65535.');
  }
  return e;
}

/** The `devices:` block for edge-agent.yaml. Throws on invalid input so a bad file is never offered. */
export function deviceYaml(d: DeviceInput): string {
  const errs = validateDevice(d);
  if (errs.length) throw new Error(errs.join(' '));
  const t = EDGE_TEMPLATES.find((x) => x.id === d.templateId)!;
  const L: string[] = ['devices:', `  - id: ${d.deviceId}`];
  if (d.link === 'rtu') {
    L.push('    profile: modbus-generic', `    port: ${d.port}`, `    baud: ${d.baud}`, '    data_bits: 8', `    stop_bits: ${d.stopBits ?? 0}`, `    parity: ${d.parity ?? 'none'}`);
  } else {
    L.push('    profile: modbus-tcp', `    host: ${d.host}`);
  }
  L.push(`    address: ${d.address}`, `    interval: ${d.intervalSec}s`, '    points:');
  for (const x of t.points) {
    const reg = d.pduBase === 'minus1' ? x.register - 1 : x.register;
    L.push(`      - {id: ${x.id}, register: ${reg}, func: ${d.readFunc ?? t.func}, type: ${x.type ?? t.type}, word_order: ${d.wordOrder ?? t.word_order}, scale: ${x.scale ?? 1}, unit: "${x.unit}", min: ${x.min}, max: ${x.max}}`);
  }
  return L.join('\n') + '\n';
}
