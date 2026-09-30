import { useEffect, useState } from 'react';
import { api, download } from '../lib/api';

interface Metric { device_id: string; point_id: string; }
interface ReportDef { metrics: Metric[]; window_hours: number; group_by: string; }
interface ReportRow { id: string; name: string; definition: ReportDef; schedule_cron: string | null; channel_id: string | null; last_run_at: string | null; }
interface PointRow { device_id: string; device_name: string; point_id: string; unit: string; }
interface Channel { id: string; type: string; target: string; enabled: boolean; }

export default function Reports() {
  const [reports, setReports] = useState<ReportRow[]>([]);
  const [channels, setChannels] = useState<Channel[]>([]);
  const [name, setName] = useState('');
  const [metrics, setMetrics] = useState<Metric[]>([{ device_id: '', point_id: '' }]);
  const [windowHours, setWindowHours] = useState(24);
  const [groupBy, setGroupBy] = useState('hour');
  const [cron, setCron] = useState('');
  const [channelId, setChannelId] = useState('');
  const [msg, setMsg] = useState('');
  const [points, setPoints] = useState<PointRow[]>([]);
  const [previewHTML, setPreviewHTML] = useState('');
  const [previewRows, setPreviewRows] = useState(0);

  const load = () => {
    api<ReportRow[]>('/v1/reports').then(setReports).catch(e => setMsg(String(e)));
    api<Channel[]>('/v1/notifications/channels').then(setChannels).catch(() => {});
    api<PointRow[]>('/v1/points').then(setPoints).catch(() => {});
  };
  useEffect(load, []);

  function setMetric(i: number, k: keyof Metric, v: string) {
    setMetrics(ms => ms.map((m, j) => (j === i ? { ...m, [k]: v } : m)));
  }

  async function submit(e: React.FormEvent) {
    e.preventDefault();
    setMsg('');
    try {
      await api('/v1/reports', {
        method: 'POST',
        body: JSON.stringify({
          name,
          definition: { metrics: metrics.filter(m => m.device_id && m.point_id), window_hours: windowHours, group_by: groupBy },
          schedule_cron: cron || '',
          channel_id: channelId || ''
        })
      });
      setName(''); setCron('');
      load();
    } catch (e2) { setMsg(String(e2)); }
  }

  async function previewNow() {
    setMsg('');
    try {
      const r = await api<{ html: string; rows: number }>('/v1/reports/preview', {
        method: 'POST',
        body: JSON.stringify({ name, definition: { metrics: metrics.filter(m => m.device_id && m.point_id), window_hours: windowHours, group_by: groupBy } })
      });
      setPreviewHTML(r.html); setPreviewRows(r.rows);
    } catch (e2) { setMsg(String(e2)); }
  }

  async function runNow(id: string) {
    setMsg('');
    try { await api(`/v1/reports/${id}/run`, { method: 'POST' }); setMsg('Report ran and delivered.'); load(); }
    catch (e2) { setMsg(String(e2)); }
  }

  return (
    <>
      <h1>Reports</h1>
      <div className="card" style={{ maxWidth: 680, marginBottom: 20 }}>
        <b>New report</b>
        <form onSubmit={submit}>
          <label>Name</label>
          <input value={name} onChange={e => setName(e.target.value)} placeholder="Weekly energy summary" required />
          <label>Metrics</label>
          {metrics.map((m, i) => (
            <div key={i} style={{ display: 'flex', gap: 8, marginBottom: 6 }}>
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
          <label>Window</label>
          <div style={{ display: 'flex', gap: 6, marginBottom: 6 }}>
            {[[24, 'Last 24h'], [168, '7 days'], [720, '30 days'], [2160, '90 days']].map(([h, l]) => (
              <button key={h} type="button" className={windowHours === h ? '' : 'ghost'} onClick={() => setWindowHours(+h)}>{l}</button>
            ))}
          </div>
          <input type="number" min={1} max={2160} value={windowHours} onChange={e => setWindowHours(+e.target.value)} />
          <label>Group by</label>
          <select value={groupBy} onChange={e => setGroupBy(e.target.value)}>
            <option value="hour">Hour</option><option value="day">Day</option>
          </select>
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
            <button type="submit">Create report</button>
          </div>
        </form>
        {msg && <p className="muted">{msg}</p>}
      </div>
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
          <b>{r.name}</b> <span className="muted">{r.definition.metrics.length} metrics, {r.definition.window_hours}h window, by {r.definition.group_by}</span>
          <div className="muted">
            {r.schedule_cron ? <>schedule {r.schedule_cron} - </> : 'on demand - '}
            {r.last_run_at ? `last run ${new Date(r.last_run_at).toLocaleString()}` : 'never run'}
          </div>
          <div style={{ display: 'flex', gap: 8, marginTop: 10 }}>
            <button onClick={() => runNow(r.id)}>Run now</button>
            <button className="ghost" onClick={() => download(`/v1/reports/${r.id}/download?format=csv`, `${r.name}.csv`).catch(e => setMsg(String(e)))}>CSV</button>
            <button className="ghost" onClick={() => download(`/v1/reports/${r.id}/download?format=html`, `${r.name}.html`).catch(e => setMsg(String(e)))}>HTML</button>
          </div>
        </div>
      ))}
      {reports.length === 0 && <p className="muted">No reports yet.</p>}
    </>
  );
}
