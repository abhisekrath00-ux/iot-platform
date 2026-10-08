import { useEffect, useState } from 'react';
import { api, download } from '../lib/api';
import ShareWithCustomer from '../components/ShareWithCustomer';

interface Metric { device_id: string; point_id: string; }
interface ReportDef { detail?: number; metrics: Metric[]; window_hours: number; group_by: string; layout?: string; agg?: string; rollup?: string; header?: string; footer?: string; theme?: string; chart?: string; page?: string; highlight?: { above?: number; below?: number; when?: string; when_color?: string }; insights?: boolean; compare?: boolean; computed?: { name: string; expr: string }[]; }
interface ReportRow { id: string; name: string; customer_id?: string | null; definition: ReportDef; schedule_cron: string | null; channel_id: string | null; last_run_at: string | null; version?: number; }
interface VersionRow { version: number; name: string; definition: ReportDef; schedule_cron: string | null; replaced_by: string; replaced_at: string; }
interface PointRow { device_id: string; device_name: string; point_id: string; unit: string; }
interface Channel { id: string; type: string; target: string; enabled: boolean; }

export default function Reports() {
  const [reports, setReports] = useState<ReportRow[]>([]);
  const [channels, setChannels] = useState<Channel[]>([]);
  const [me, setMe] = useState<{ role: string; customer_id?: string } | null>(null);
  useEffect(() => { api<{ role: string; customer_id?: string }>('/v1/me').then(setMe).catch(() => {}); }, []);
  const scoped = !!me?.customer_id;
  const [name, setName] = useState('');
  const [metrics, setMetrics] = useState<Metric[]>([{ device_id: '', point_id: '' }]);
  const [windowHours, setWindowHours] = useState(24);
  const [groupBy, setGroupBy] = useState('hour');
  const [layout, setLayout] = useState('');
  const [agg, setAgg] = useState('avg');
  const [rollup, setRollup] = useState('');
  const [header, setHeader] = useState('');
  const [footer, setFooter] = useState('');
  const [theme, setTheme] = useState('light');
  const [chart, setChart] = useState('');
  const [page, setPage] = useState('');
  const [hiAbove, setHiAbove] = useState('');
  const [hiBelow, setHiBelow] = useState('');
  const [hiWhen, setHiWhen] = useState('');
  const [hiWhenColor, setHiWhenColor] = useState('red');
  const [detail, setDetail] = useState('');
  const [insights, setInsights] = useState(false);
  const [compare, setCompare] = useState(false);
  const [computed, setComputed] = useState<{ name: string; expr: string }[]>([]);
  const [devices, setDevices] = useState<{ id: string; name: string }[]>([]);
  const [pw, setPw] = useState<Record<string, { w?: string; g?: string; d?: string; s?: string; a?: string }>>({});
  const [opts, setOpts] = useState<{ sites: { id: string; name: string }[]; assets: { id: string; name: string; site_ids: string[] }[]; devices: { id: string; name: string; site_id: string; asset_id: string | null }[] }>({ sites: [], assets: [], devices: [] });
  const [cron, setCron] = useState('');
  const [editingId, setEditingId] = useState('');
  const [hist, setHist] = useState<{ id: string; current: number; versions: VersionRow[] } | null>(null);
  const [channelId, setChannelId] = useState('');
  const [msg, setMsg] = useState('');
  const [points, setPoints] = useState<PointRow[]>([]);
  const [dragFrom, setDragFrom] = useState<number | null>(null);
  const [dropOver, setDropOver] = useState(false);
  const addMetric = (device_id: string, point_id: string) => setMetrics(ms => (ms.length === 1 && !ms[0].device_id ? [{ device_id, point_id }] : ms.some(m => m.device_id === device_id && m.point_id === point_id) ? ms : [...ms, { device_id, point_id }]));
  const reorder = (from: number, to: number) => setMetrics(ms => { if (from === to || from < 0 || to < 0 || from >= ms.length || to >= ms.length) return ms; const n = [...ms]; const [x] = n.splice(from, 1); n.splice(to, 0, x); return n; });
  const [previewHTML, setPreviewHTML] = useState('');
  const [previewRows, setPreviewRows] = useState(0);

  const load = () => {
    api<ReportRow[]>('/v1/reports').then(setReports).catch(e => setMsg(String(e)));
    api<Channel[]>('/v1/notifications/channels').then(setChannels).catch(() => {});
    api<PointRow[]>('/v1/points').then(setPoints).catch(() => {});
    api<{ id: string; name: string }[]>('/v1/devices').then(setDevices).catch(() => {});
    api<typeof opts>('/v1/reports/options').then(setOpts).catch(() => {});
  };
  useEffect(load, []);

  function setMetric(i: number, k: keyof Metric, v: string) {
    setMetrics(ms => ms.map((m, j) => (j === i ? { ...m, [k]: v } : m)));
  }

  const num = (v: string) => (v.trim() !== '' && Number.isFinite(Number(v)) ? Number(v) : undefined);
  const highlight = () => { const above = num(hiAbove), below = num(hiBelow), when = hiWhen.trim() || undefined; return above === undefined && below === undefined && !when ? undefined : { above, below, when, when_color: when && hiWhenColor === 'amber' ? 'amber' : undefined }; };
  const buildDef = () => ({ detail: num(detail) && highlight() ? num(detail) : undefined, metrics: metrics.filter(m => m.device_id && m.point_id), window_hours: windowHours, group_by: groupBy, layout, agg, rollup: rollup || undefined, header: header || undefined, footer: footer || undefined, theme: theme === 'dark' ? 'dark' : undefined, chart: chart || undefined, insights: insights || undefined, compare: (insights && compare) || undefined, computed: layout === 'matrix' ? computed.filter(c => c.name && c.expr) : undefined, page: page || undefined, highlight: highlight() });

  async function submit(e: React.FormEvent) {
    e.preventDefault();
    setMsg('');
    try {
      await api(editingId ? `/v1/reports/${editingId}` : '/v1/reports', {
        method: editingId ? 'PUT' : 'POST',
        body: JSON.stringify({
          name,
          definition: buildDef(),
          schedule_cron: cron || '',
          channel_id: channelId || ''
        })
      });
      setName(''); setCron(''); setEditingId(''); setHist(null);
      load();
    } catch (e2) { setMsg(String(e2)); }
  }

  async function previewNow() {
    setMsg('');
    try {
      const r = await api<{ html: string; rows: number }>('/v1/reports/preview', {
        method: 'POST',
        body: JSON.stringify({ name, definition: buildDef() })
      });
      setPreviewHTML(r.html); setPreviewRows(r.rows);
    } catch (e2) { setMsg(String(e2)); }
  }

  // Load a saved report into the form; saving writes a new version and keeps the old one.
  function edit(r: ReportRow) {
    const d = r.definition;
    setEditingId(r.id); setName(r.name); setMetrics(d.metrics.length ? d.metrics : [{ device_id: '', point_id: '' }]);
    setWindowHours(d.window_hours); setGroupBy(d.group_by); setLayout(d.layout ?? ''); setAgg(d.agg ?? 'avg'); setRollup(d.rollup ?? '');
    setDetail(d.detail ? String(d.detail) : ''); setHeader(d.header ?? ''); setFooter(d.footer ?? ''); setTheme(d.theme ?? 'light'); setChart(d.chart ?? ''); setPage(d.page ?? ''); setHiAbove(d.highlight?.above != null ? String(d.highlight.above) : ''); setHiBelow(d.highlight?.below != null ? String(d.highlight.below) : ''); setHiWhen(d.highlight?.when ?? ''); setHiWhenColor(d.highlight?.when_color ?? 'red'); setInsights(!!d.insights); setCompare(!!d.compare); setComputed(d.computed ?? []); setCron(r.schedule_cron ?? ''); setChannelId(r.channel_id ?? '');
    setMsg(''); window.scrollTo({ top: 0 });
  }
  function cancelEdit() { setEditingId(''); setName(''); setCron(''); setMsg(''); }
  async function showHistory(id: string) {
    if (hist?.id === id) { setHist(null); return; }
    try { const h = await api<{ current: number; versions: VersionRow[] }>(`/v1/reports/${id}/versions`); setHist({ id, ...h }); }
    catch (e2) { setMsg(String(e2)); }
  }
  async function restore(id: string, v: number) {
    setMsg('');
    try { await api(`/v1/reports/${id}/versions/${v}/restore`, { method: 'POST' }); setHist(null); setMsg(`Restored version ${v} as a new version.`); load(); }
    catch (e2) { setMsg(String(e2)); }
  }

  async function runNow(id: string) {
    setMsg('');
    try { await api(`/v1/reports/${id}/run`, { method: 'POST' }); setMsg('Report ran and delivered.'); load(); }
    catch (e2) { setMsg(String(e2)); }
  }

  const singleDevice = (d: ReportDef) => !(d.computed?.length) && d.metrics.length > 0 && d.metrics.every(m => m.device_id === d.metrics[0].device_id);
  const qs = (id: string) => `${pw[id]?.w ? `window_hours=${encodeURIComponent(pw[id].w!)}&` : ''}${pw[id]?.g ? `group_by=${pw[id].g}&` : ''}${pw[id]?.s ? `site=${encodeURIComponent(pw[id].s!)}&` : ''}${pw[id]?.a ? `asset=${encodeURIComponent(pw[id].a!)}&` : ''}${pw[id]?.d ? `device=${encodeURIComponent(pw[id].d!)}&` : ''}`;
  const setP = (id: string, patch: { w?: string; g?: string; d?: string; s?: string; a?: string }) => setPw(p => ({ ...p, [id]: { ...p[id], ...patch } }));
  const cascadeDevices = (id: string) => opts.devices.filter(d => (!pw[id]?.s || d.site_id === pw[id].s) && (!pw[id]?.a || d.asset_id === pw[id].a));
  return (
    <>
      <h1>Reports</h1>
      {!scoped && <div className="card" style={{ maxWidth: 680, marginBottom: 20 }}>
        <b>New report</b>
        <form onSubmit={submit}>
          <label>Name</label>
          <input value={name} onChange={e => setName(e.target.value)} placeholder="Weekly energy summary" required />
          <label>Design surface</label>
          <div data-testid="palette" style={{ display: 'flex', flexWrap: 'wrap', gap: 6, marginBottom: 8 }}>
            {points.map(p => (
              <span key={`${p.device_id}|${p.point_id}`} draggable className="ghost" title="Drag onto the report, or click to add"
                onDragStart={e => { e.dataTransfer.setData('text/hx-point', `${p.device_id}|${p.point_id}`); e.dataTransfer.effectAllowed = 'copy'; }}
                onClick={() => addMetric(p.device_id, p.point_id)}
                style={{ cursor: 'grab', border: '1px solid var(--border, #444)', borderRadius: 6, padding: '2px 8px', fontSize: 12 }}>
                {p.device_name} / {p.point_id}
              </span>
            ))}
          </div>
          <div data-testid="dropzone"
            onDragOver={e => { if (e.dataTransfer.types.includes('text/hx-point')) { e.preventDefault(); setDropOver(true); } }}
            onDragLeave={() => setDropOver(false)}
            onDrop={e => { setDropOver(false); const v = e.dataTransfer.getData('text/hx-point'); if (v) { e.preventDefault(); const [d, pt] = v.split('|'); addMetric(d, pt); } }}
            style={{ border: `2px dashed ${dropOver ? '#4c8dff' : 'var(--border, #444)'}`, borderRadius: 8, padding: 8, marginBottom: 8 }}>
          <label>Metrics (drag a point here; drag a row's handle to reorder)</label>
          {metrics.map((m, i) => (
            <div key={i} draggable={dragFrom === i}
              onDragOver={e => { if (dragFrom !== null) e.preventDefault(); }}
              onDrop={e => { if (dragFrom !== null) { e.preventDefault(); e.stopPropagation(); reorder(dragFrom, i); setDragFrom(null); } }}
              style={{ display: 'flex', gap: 8, marginBottom: 6 }}>
              <span role="button" aria-label={`Drag metric ${i + 1}`} title="Drag to reorder" style={{ cursor: 'grab', userSelect: 'none', alignSelf: 'center' }}
                onMouseDown={() => setDragFrom(i)} onMouseUp={() => setDragFrom(null)}>::</span>
              <select value={m.device_id && m.point_id ? `${m.device_id}|${m.point_id}` : ''} required
                onChange={e => { const [d, p] = e.target.value.split('|'); setMetrics(ms => ms.map((x, j) => (j === i ? { device_id: d, point_id: p } : x))); }}>
                <option value="">Choose a device point...</option>
                {points.map(p => <option key={`${p.device_id}|${p.point_id}`} value={`${p.device_id}|${p.point_id}`}>{p.device_name} / {p.point_id}{p.unit ? ` (${p.unit})` : ''}</option>)}
              </select>
              <button type="button" className="ghost" onClick={() => setMetrics(ms => ms.filter((_, j) => j !== i))} disabled={metrics.length === 1}>x</button>
            </div>
          ))}
          {points.length === 0 && <p className="muted">No device points yet. Onboard a device first.</p>}
          <button type="button" className="ghost" onClick={() => setMetrics(ms => [...ms, { device_id: '', point_id: '' }])}>+ metric</button>
          </div>
          <label>Window</label>
          <div style={{ display: 'flex', gap: 6, marginBottom: 6 }}>
            {[[24, 'Last 24h'], [168, '7 days'], [720, '30 days'], [2160, '90 days']].map(([h, l]) => (
              <button key={h} type="button" className={windowHours === h ? '' : 'ghost'} onClick={() => setWindowHours(+h)}>{l}</button>
            ))}
          </div>
          <input type="number" min={1} max={2160} value={windowHours} onChange={e => setWindowHours(+e.target.value)} />
          <label>Group by</label>
          <select value={groupBy} onChange={e => setGroupBy(e.target.value)}>
            <option value="15min">15 minutes</option><option value="hour">Hour</option><option value="day">Day</option><option value="week">Week</option>
          </select>
          <label>Summary by</label>
          <select value={rollup} onChange={e => setRollup(e.target.value)} aria-label="Summary grouping">
            <option value="">No summary</option><option value="asset">Asset (subtotal per asset and point, plus totals)</option><option value="site">Site (subtotal per site and point, plus totals)</option><option value="site>asset">Site, then asset inside each site (nested subtotals)</option><option value="asset>site">Asset, then site inside each asset (nested subtotals)</option>
          </select>
          <label><input type="checkbox" checked={insights} onChange={e => { setInsights(e.target.checked); if (!e.target.checked) setCompare(false); }} style={{ width: 'auto' }} /> Add an insights section (trend, peak, unusual buckets)</label>
          <label><input type="checkbox" checked={compare} disabled={!insights || windowHours > 24 * 45} onChange={e => setCompare(e.target.checked)} style={{ width: 'auto' }} /> Compare with the previous window (windows up to 45 days)</label>
          <label>Page header and footer (optional, up to 80 characters each; printed on every PDF page)</label>
          <div style={{ display: 'flex', gap: 6 }}>
            <input maxLength={80} placeholder="Header text" aria-label="Report header" value={header} onChange={e => setHeader(e.target.value)} />
            <input maxLength={80} placeholder="Footer text" aria-label="Report footer" value={footer} onChange={e => setFooter(e.target.value)} />
          </div>
          <label>Chart in HTML and Excel reports</label>
          <select aria-label="Report chart" value={chart} onChange={e => setChart(e.target.value)}><option value="">None</option><option value="line">Line</option><option value="area">Area</option><option value="bar">Bar</option><option value="scatter">Scatter</option><option value="gauge">Gauge (latest)</option><option value="pie">Pie (share of total)</option></select>
                    <label>Highlight cells in the per-point tables (HTML): red above, amber below. Leave empty for none</label>
          <div style={{ display: 'flex', gap: 6 }}>
            <input type="number" step="any" placeholder="Red above" aria-label="Highlight above" value={hiAbove} onChange={e => setHiAbove(e.target.value)} />
            <input type="number" step="any" placeholder="Amber below" aria-label="Highlight below" value={hiBelow} onChange={e => setHiBelow(e.target.value)} />
          </div>
          <div style={{ display: 'flex', gap: 8, marginTop: 8, flexWrap: 'wrap' }}>
            <input placeholder="Rule, e.g. if({row.avg} > 50 and {row.max} < 100, 1, 0)" aria-label="Highlight rule" value={hiWhen} onChange={e => setHiWhen(e.target.value)} style={{ flex: '1 1 260px' }} />
            <select aria-label="Highlight rule colour" value={hiWhenColor} onChange={e => setHiWhenColor(e.target.value)}><option value="red">Rule colour: red</option><option value="amber">Rule colour: amber</option></select>
          <input type="number" min={1} max={20} placeholder="Drill-through: readings per highlighted bucket (1-20)" aria-label="Drill-through readings" value={detail} onChange={e => setDetail(e.target.value)} style={{ flex: '1 1 260px' }} />
          </div>
          <label>Page theme (PDF and HTML)</label>
          <select aria-label="Report theme" value={theme} onChange={e => setTheme(e.target.value)}><option value="light">Light</option><option value="dark">Dark</option></select>
          <label>PDF page size</label>
          <select aria-label="PDF page" value={page} onChange={e => setPage(e.target.value)}>
            <option value="">A4 portrait (default)</option><option value="a4-landscape">A4 landscape</option><option value="letter">Letter portrait</option><option value="letter-landscape">Letter landscape</option>
          </select>
          <label>Layout</label>
          <select value={layout} onChange={e => setLayout(e.target.value)}>
            <option value="">One table per point</option><option value="matrix">Matrix (time rows, point columns)</option>
          </select>
          {layout === 'matrix' && <select value={agg} onChange={e => setAgg(e.target.value)} aria-label="Matrix value">
            <option value="avg">Average</option><option value="min">Min</option><option value="max">Max</option><option value="sum">Sum</option>
          </select>}
          {layout === 'matrix' && <div>
            <label>Computed columns (use {'{device.point}'} with + - * /, e.g. {'{meter-1.kwh} / {line-a.units}'})</label>
            {computed.map((c, i) => (
              <div key={i} style={{ display: 'flex', gap: 6, marginBottom: 6 }}>
                <input placeholder="column name" aria-label={`Computed name ${i + 1}`} value={c.name} onChange={e => setComputed(computed.map((x, j) => j === i ? { ...x, name: e.target.value } : x))} />
                <input placeholder="{device.point} / {device.point}" aria-label={`Computed expression ${i + 1}`} value={c.expr} onChange={e => setComputed(computed.map((x, j) => j === i ? { ...x, expr: e.target.value } : x))} />
                <button type="button" className="ghost" onClick={() => setComputed(computed.filter((_, j) => j !== i))}>Remove</button>
              </div>))}
            {computed.length < 5 && <button type="button" className="ghost" onClick={() => setComputed([...computed, { name: '', expr: '' }])}>Add computed column</button>}
          </div>}
          <label>Schedule (5-field cron, empty = on demand)</label>
          <div style={{ display: 'flex', gap: 6, marginBottom: 6, flexWrap: 'wrap' }}>
            {[['', 'On demand'], ['0 8 * * *', 'Daily 8:00'], ['0 8 * * 1', 'Mondays 8:00'], ['0 8 1 * *', 'Monthly 1st 8:00']].map(([c, l]) => (
              <button key={l} type="button" className={cron === c ? '' : 'ghost'} onClick={() => setCron(c)}>{l}</button>
            ))}
          </div>
          <input value={cron} onChange={e => setCron(e.target.value)} placeholder="custom: 0 6 * * 1" />
          <label>Deliver to</label>
          <select value={channelId} onChange={e => setChannelId(e.target.value)}>
            <option value="">None (run history only)</option>
            {channels.map(c => <option key={c.id} value={c.id}>{c.type}: {c.target}</option>)}
          </select>
          <div style={{ marginTop: 14, display: 'flex', gap: 8 }}>
            <button type="button" className="ghost" onClick={previewNow}>Preview</button>
            <button type="submit">{editingId ? 'Save changes (new version)' : 'Create report'}</button>
            {editingId && <button type="button" className="ghost" onClick={cancelEdit}>Cancel edit</button>}
          </div>
        </form>
        {msg && <p className="muted">{msg}</p>}
      </div>}
      {scoped && msg && <p className="muted">{msg}</p>}
      {previewHTML && (
        <div className="card" style={{ marginBottom: 20, padding: 0, overflow: 'hidden' }}>
          <div style={{ padding: '12px 18px', display: 'flex', justifyContent: 'space-between' }}>
            <b>Preview <span className="muted">({previewRows} rows)</span></b>
            <button className="ghost" onClick={() => setPreviewHTML('')}>Close</button>
          </div>
          <iframe title="report preview" sandbox="" srcDoc={previewHTML} style={{ width: '100%', height: 420, border: 0, background: '#fff' }} />
        </div>
      )}
      {reports.map(r => (
        <div key={r.id} className="card" style={{ marginBottom: 10 }}>
          <b>{r.name}</b> <span className="muted">v{r.version ?? 1} - {r.definition.metrics.length} metrics, {r.definition.window_hours}h window, by {r.definition.group_by}{r.definition.rollup ? `, summary by ${r.definition.rollup}` : ''}</span>
          <div className="muted">
            {r.schedule_cron ? <>schedule {r.schedule_cron} - </> : 'on demand - '}
            {r.last_run_at ? `last run ${new Date(r.last_run_at).toLocaleString()}` : 'never run'}
          </div>
          {!scoped && <div style={{ display: 'flex', gap: 8, marginTop: 10, alignItems: 'center', flexWrap: 'wrap' }} aria-label="Run parameters">
            <span className="muted">Parameters for downloads:</span>
            <input type="number" min={1} max={2160} style={{ width: 90 }} placeholder={`${r.definition.window_hours}h`} aria-label="Window hours"
              value={pw[r.id]?.w ?? ''} onChange={e => setPw({ ...pw, [r.id]: { ...pw[r.id], w: e.target.value } })} />
            <select style={{ width: 150 }} aria-label="Group by override" value={pw[r.id]?.g ?? ''} onChange={e => setPw({ ...pw, [r.id]: { ...pw[r.id], g: e.target.value } })}>
              <option value="">{r.definition.group_by}</option><option value="15min">15 minutes</option><option value="hour">Hour</option><option value="day">Day</option><option value="week">Week</option>
            </select>
{singleDevice(r.definition) && <>
              <select style={{ width: 160 }} aria-label="Site parameter" value={pw[r.id]?.s ?? ''} onChange={e => setP(r.id, { s: e.target.value, a: '', d: '' })}>
                <option value="">All sites</option>
                {opts.sites.map(x => <option key={x.id} value={x.id}>{x.name || x.id}</option>)}
              </select>
              <select style={{ width: 160 }} aria-label="Asset parameter" value={pw[r.id]?.a ?? ''} onChange={e => setP(r.id, { a: e.target.value, d: '' })}>
                <option value="">All assets{pw[r.id]?.s ? ' in site' : ''}</option>
                {opts.assets.filter(x => !pw[r.id]?.s || x.site_ids.includes(pw[r.id].s!)).map(x => <option key={x.id} value={x.id}>{x.name || x.id}</option>)}
              </select>
              <select style={{ width: 240 }} aria-label="Device override" value={pw[r.id]?.d ?? ''} onChange={e => setP(r.id, { d: e.target.value })}>
                <option value="">{pw[r.id]?.s || pw[r.id]?.a ? `All ${cascadeDevices(r.id).length} matching devices` : r.definition.metrics[0].device_id}</option>
                {(pw[r.id]?.s || pw[r.id]?.a ? cascadeDevices(r.id) : devices.filter(d => d.id !== r.definition.metrics[0].device_id)).map(d => <option key={d.id} value={d.id}>{d.name || d.id}</option>)}
              </select>
              {(pw[r.id]?.s || pw[r.id]?.a) && !pw[r.id]?.d && cascadeDevices(r.id).length > 10 && <span className="muted" role="alert">More than 10 devices match: pick one or narrow the choice.</span>}
            </>}
          </div>}
          <div style={{ display: 'flex', gap: 8, marginTop: 10 }}>
            {!scoped && <><button onClick={() => runNow(r.id)}>Run now</button>
            <button className="ghost" onClick={() => edit(r)}>Edit</button>
            <button className="ghost" onClick={() => showHistory(r.id)} aria-expanded={hist?.id === r.id}>History</button></>}
            <button className="ghost" onClick={() => download(`/v1/reports/${r.id}/download?${qs(r.id)}format=csv`, `${r.name}.csv`).catch(e => setMsg(String(e)))}>CSV</button>
            <button className="ghost" onClick={() => download(`/v1/reports/${r.id}/download?${qs(r.id)}format=html`, `${r.name}.html`).catch(e => setMsg(String(e)))}>HTML</button>
            <button className="ghost" onClick={() => download(`/v1/reports/${r.id}/download?${qs(r.id)}format=pdf`, `${r.name}.pdf`).catch(e => setMsg(String(e)))}>PDF</button>
            <button className="ghost" onClick={() => download(`/v1/reports/${r.id}/download?${qs(r.id)}format=xlsx`, `${r.name}.xlsx`).catch(e => setMsg(String(e)))}>Excel</button>
            <button className="ghost" onClick={() => download(`/v1/reports/${r.id}/download?${qs(r.id)}format=docx`, `${r.name}.docx`).catch(e => setMsg(String(e)))}>Word</button>
            <button className="ghost" onClick={() => download(`/v1/reports/${r.id}/download?${qs(r.id)}format=pptx`, `${r.name}.pptx`).catch(e => setMsg(String(e)))}>PowerPoint</button>
            <button className="ghost" onClick={() => download(`/v1/reports/${r.id}/download?${qs(r.id)}format=xml`, `${r.name}.xml`).catch(e => setMsg(String(e)))}>XML</button>
            {me?.role === 'admin' && !scoped && <ShareWithCustomer path={`/v1/reports/${r.id}/customer`} value={r.customer_id} onDone={load} onError={setMsg} />}
          </div>
          {hist?.id === r.id && (
            <div style={{ marginTop: 10 }} role="region" aria-label="Report history">
              <b>Version history</b> <span className="muted">(current is v{hist.current})</span>
              {hist.versions.length === 0 && <p className="muted">No earlier versions. Editing the report saves the current one here first.</p>}
              {hist.versions.map(v => (
                <div key={v.version} style={{ display: 'flex', gap: 10, alignItems: 'center', marginTop: 6, flexWrap: 'wrap' }}>
                  <span>v{v.version}</span>
                  <span className="muted">{v.name}, {v.definition.metrics.length} metrics, {v.definition.window_hours}h, by {v.definition.group_by}; replaced {new Date(v.replaced_at).toLocaleString()}</span>
                  <button className="ghost" onClick={() => restore(r.id, v.version)}>Restore as new version</button>
                </div>
              ))}
            </div>
          )}
        </div>
      ))}
      {reports.length === 0 && <p className="muted">{scoped ? 'No reports have been shared with you yet.' : 'No reports yet.'}</p>}
    </>
  );
}
