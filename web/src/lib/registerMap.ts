// Modbus register-map import, export and checks. Nothing here saves anything:
// the Profiles page loads the result into the form for the user to review.
import { EDGE_TEMPLATES } from './edgeSetup';

export interface MapPoint { id: string; register: number; func: number; type: string; word_order: string; scale: number; unit: string; min: number; max: number; }
export interface Issue { row: number; text: string; }

const TYPES = ['u16', 'i16', 'u32', 'i32', 'f32', 'bool'];
const ORDERS = ['abcd', 'badc', 'cdab', 'dcba'];
const WIDTH: Record<string, number> = { u16: 1, i16: 1, bool: 1, u32: 2, i32: 2, f32: 2 };
export const CSV_COLUMNS = ['id', 'register', 'func', 'type', 'word_order', 'scale', 'unit', 'min', 'max'];

function splitLine(line: string): string[] {
  const out: string[] = []; let cur = ''; let q = false;
  for (let i = 0; i < line.length; i++) {
    const c = line[i];
    if (q) { if (c === '"' && line[i + 1] === '"') { cur += '"'; i++; } else if (c === '"') q = false; else cur += c; }
    else if (c === '"') q = true;
    else if (c === ',') { out.push(cur); cur = ''; }
    else cur += c;
  }
  out.push(cur);
  return out.map((s) => s.trim());
}

const num = (s: string | undefined, d: number) => (s === undefined || s === '' ? d : Number(s));

/** Parse CSV text with a header row. Column order is free; missing optional columns get defaults. */
export function parseCsv(text: string): { points: MapPoint[]; issues: Issue[] } {
  const lines = text.replace(/^\uFEFF/, '').split(/\r?\n/).filter((l) => l.trim() !== '');
  const issues: Issue[] = [];
  if (!lines.length) return { points: [], issues: [{ row: 0, text: 'The file is empty.' }] };
  const head = splitLine(lines[0]).map((h) => h.toLowerCase().replace(/[\s-]+/g, '_'));
  for (const need of ['id', 'register']) if (!head.includes(need)) issues.push({ row: 1, text: `Header is missing the "${need}" column.` });
  if (issues.length) return { points: [], issues };
  const points: MapPoint[] = [];
  lines.slice(1).forEach((ln, i) => {
    const c = splitLine(ln); const g = (k: string) => { const j = head.indexOf(k); return j < 0 ? undefined : c[j]; };
    points.push({ id: g('id') ?? '', register: num(g('register'), NaN), func: num(g('func'), 4), type: (g('type') || 'u16').toLowerCase(), word_order: (g('word_order') || 'abcd').toLowerCase(), scale: num(g('scale'), 1), unit: g('unit') ?? '', min: num(g('min'), 0), max: num(g('max'), 1000000) });
    void i;
  });
  return { points, issues: validateMap(points) };
}

/** Parse a JSON array of points, or an object with a "points" array (the shape /v1/profiles returns). */
export function parseJson(text: string): { points: MapPoint[]; issues: Issue[] } {
  let v: unknown;
  try { v = JSON.parse(text); } catch { return { points: [], issues: [{ row: 0, text: 'Not valid JSON.' }] }; }
  const arr = Array.isArray(v) ? v : (v as { points?: unknown })?.points;
  if (!Array.isArray(arr)) return { points: [], issues: [{ row: 0, text: 'Expected an array of points or an object with a "points" array.' }] };
  const points = arr.map((r: Record<string, unknown>) => ({
    id: String(r.id ?? ''), register: Number(r.register ?? NaN), func: Number(r.func ?? 4), type: String(r.type ?? 'u16').toLowerCase(), word_order: String(r.word_order ?? 'abcd').toLowerCase(),
    scale: Number(r.scale ?? 1), unit: String(r.unit ?? ''), min: Number(r.min ?? 0), max: Number(r.max ?? 1000000),
  }));
  return { points, issues: validateMap(points) };
}

export function parseAny(text: string): { points: MapPoint[]; issues: Issue[] } {
  return text.trim().startsWith('[') || text.trim().startsWith('{') ? parseJson(text) : parseCsv(text);
}

export function toCsv(points: MapPoint[]): string {
  const q = (s: string) => (/[",\n]/.test(s) ? `"${s.replace(/"/g, '""')}"` : s);
  return [CSV_COLUMNS.join(','), ...points.map((p) => [p.id, p.register, p.func, p.type, p.word_order, p.scale, p.unit, p.min, p.max].map((x) => q(String(x))).join(','))].join('\n') + '\n';
}

/** Problems a user should fix before saving. Row numbers are 1-based data rows (header excluded). */
export function validateMap(points: MapPoint[]): Issue[] {
  const out: Issue[] = [];
  const ids = new Map<string, number>();
  const used: { reg: number; end: number; func: number; row: number; id: string }[] = [];
  points.forEach((p, i) => {
    const row = i + 1; const add = (t: string) => out.push({ row, text: `${p.id || '(no id)'}: ${t}` });
    if (!/^[A-Za-z][A-Za-z0-9_.-]{0,63}$/.test(p.id)) add('id must start with a letter and use letters, digits, _ . - only.');
    else if (ids.has(p.id)) add(`duplicate id (also row ${ids.get(p.id)}).`); else ids.set(p.id, row);
    if (!Number.isInteger(p.register) || p.register < 0 || p.register > 65535) add('register must be a whole number 0-65535.');
    if (![1, 2, 3, 4].includes(p.func)) add('func must be 1 (coil), 2 (discrete), 3 (holding) or 4 (input).');
    if (!TYPES.includes(p.type)) add(`type must be one of ${TYPES.join(', ')}.`);
    else {
      if ((p.func === 1 || p.func === 2) && p.type !== 'bool') add('coils and discrete inputs must use type bool.');
      if (p.type === 'bool' && p.func > 2) add('bool is only for coils and discrete inputs.');
      const end = p.register + (WIDTH[p.type] ?? 1) - 1;
      if (end > 65535) add('register range runs past 65535.');
      for (const u of used) if (u.func === p.func && p.register <= u.end && end >= u.reg) add(`registers ${p.register}-${end} overlap ${u.id} (row ${u.row}).`);
      used.push({ reg: p.register, end, func: p.func, row, id: p.id });
    }
    if (!ORDERS.includes(p.word_order)) add(`word_order must be one of ${ORDERS.join(', ')}.`);
    if (!Number.isFinite(p.scale) || p.scale === 0) add('scale must be a non-zero number.');
    if (!Number.isFinite(p.min) || !Number.isFinite(p.max) || p.max <= p.min) add('max must be greater than min (the valid range is how bad readings are rejected).');
  });
  if (!points.length) out.push({ row: 0, text: 'No points found.' });
  return out;
}

export function templatePoints(templateId: string): MapPoint[] {
  const t = EDGE_TEMPLATES.find((x) => x.id === templateId);
  if (!t) return [];
  return t.points.map((p) => ({ id: p.id, register: p.register, func: t.func, type: t.type, word_order: t.word_order, scale: 1, unit: p.unit, min: p.min, max: p.max }));
}
