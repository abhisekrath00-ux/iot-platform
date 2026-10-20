// Edge setup helpers: built-in templates and the device block of edge-agent.yaml.
// Template numbers come from docs/devices/*.md (never run on real hardware).

export interface TplPoint { id: string; register: number; unit: string; min: number; max: number; scale?: number; type?: string; }
export interface EdgeTemplate { id: string; name: string; link: 'rtu' | 'tcp'; func: number; type: string; word_order: string; points: TplPoint[]; note: string; provisional?: string;
  // wordOrderUnverified: the vendor documents do not state the multi-register word order, so the
  // template's word_order is only a suggestion - the UI must make the installer choose it explicitly
  // (from their exact model's documentation or a live display check) before any YAML is generated.
  wordOrderUnverified?: boolean;
  // pduBaseUnverified: the vendor lists register numbers but does not state whether the on-wire
  // (PDU) address is the listed number or the listed number minus 1. The UI must make the
  // installer pick explicitly before any YAML is generated.
  pduBaseUnverified?: boolean;
  // funcUnverified: the vendor does not state the read function code (FC 03 holding vs FC 04
  // input). The UI must make the installer pick explicitly; the emitted YAML uses the choice.
  funcUnverified?: boolean; }

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
    note: 'From the official DIRIS A-40 Modbus communication table CMT-1.7.4B (docs/devices/socomec-diris-a40.md). Reads load-1 instantaneous values only; scaled integers (V 10e-2, mA, mHz, PF /1000) carry their scale. Energy counters and loads 2-3 not in this template yet. Read-only; never run on a real meter.',
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
    id: 'carlo-gavazzi-em340', name: 'Carlo Gavazzi EM340 (three-phase DIN-rail meter)', link: 'rtu', func: 4, type: 'i32', word_order: 'cdab',
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
];



export interface DeviceInput {
  deviceId: string; templateId: string; link: 'rtu' | 'tcp';
  port?: string; baud?: number; parity?: 'none' | 'even' | 'odd'; host?: string; tcpPort?: number; address: number; intervalSec: number; provisionalConfirmed?: boolean; wordOrder?: string; pduBase?: 'listed' | 'minus1'; readFunc?: 3 | 4;
}

export function validateDevice(d: DeviceInput): string[] {
  const e: string[] = [];
  if (!/^[a-z0-9][a-z0-9_-]{0,39}$/.test(d.deviceId)) e.push('Device id: lowercase letters, digits, - and _ only (max 40).');
  const tpl = EDGE_TEMPLATES.find((t) => t.id === d.templateId);
  if (!tpl) e.push('Pick a template.');
  if (tpl?.provisional && !d.provisionalConfirmed) e.push(`Provisional template: ${tpl.provisional}`);
  if (tpl?.wordOrderUnverified && !d.wordOrder) e.push('Word order: the vendor does not document it - choose it explicitly from your model\'s documentation or a display check against the meter.');
  if (tpl?.pduBaseUnverified && !d.pduBase) e.push('Register base: the vendor numbers registers from 1 but does not document whether the on-wire address is the listed number or the listed number minus 1 - choose it explicitly from your model\'s documentation or a live read of a known value (e.g. frequency at 3110).');
  if (tpl?.funcUnverified && !d.readFunc) e.push('Read function: the vendor does not document whether these registers are read with FC 03 (holding) or FC 04 (input) - choose it explicitly from your model\'s documentation or a gateway/scan tool that shows which one the meter answers.');
  if (!Number.isInteger(d.address) || d.address < 1 || d.address > 247) e.push('Modbus address must be 1-247.');
  if (!(d.intervalSec >= 1 && d.intervalSec <= 3600)) e.push('Read interval must be 1-3600 seconds.');
  if (d.link === 'rtu') {
    if (!d.port || !/^(COM\d{1,3}|\/dev\/[A-Za-z0-9._\/-]+)$/.test(d.port)) e.push('Serial port looks wrong (COM3 or /dev/ttyUSB0).');
    if (![1200, 2400, 4800, 9600, 19200, 38400, 57600, 115200].includes(d.baud ?? 0)) e.push('Pick a standard baud rate.');
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
    L.push('    profile: modbus-generic', `    port: ${d.port}`, `    baud: ${d.baud}`, '    data_bits: 8', '    stop_bits: 0', `    parity: ${d.parity ?? 'none'}`);
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
