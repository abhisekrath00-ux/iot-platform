import { useEffect, useState } from 'react';
import { api } from '../lib/api';

interface Metric { device_id: string; point_id: string; }
interface ReportDef { metrics: Metric[]; window_hours: number; group_by: string; }
interface ReportRow { id: string; name: string; definition: ReportDef; schedule_cron: string | null; channel_id: string | null; last_run_at: string | null; }
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

  const load = () => {
    api<ReportRow[]>('/v1/reports').then(setReports).catch(e => setMsg(String(e)));
    api<Channel[]>('/v1/notifications/channels').then(setChannels).catch(() => {});
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

  async function runNow(id: string) {
    setMsg('');
    try { await api(`/v1/reports/${id}/run`, { method: 'POST' }); setMsg('Report ran and delivered.'); load(); }
    catch (e2) { setMsg(String(e2)); }
  }

  return (
    <>
      <h1>Reports</h1>
      <div className="card" style={{ maxWidth: 640, marginBottom: 20 }}>
        <b>New report</b>
        <form onSubmit={submit}>
          <label>Name</label>
          <input value={name} onChange={e => setName(e.target.value)} placeholder="Weekly energy summary" required />
          <label>Metrics (device id / point id)</label>
          {metrics.map((m, i) => (
            <div key={i} style={{ display: 'flex', gap: 8, marginBottom: 6 }}>
              <input value={m.device_id} onChange={e => setMetric(i, 'device_id', e.target.value)} placeholder="meter-1" required />
              <input value={m.point_id} onChange={e => setMetric(i, 'point_id', e.target.value)} placeholder="kwh" required />
              <button type="button" onClick={() => setMetrics(ms => ms.filter((_, j) => j !== i))} disabled={metrics.length === 1}>x</button>
            </div>
          ))}
          <button type="button" onClick={() => setMetrics(ms => [...ms, { device_id: '', point_id: '' }])}>+ metric</button>
          <label>Window (hours)</label>
          <input type="number" min={1} max={2160} value={windowHours} onChange={e => setWindowHours(+e.target.value)} />
          <label>Group by</label>
          <select value={groupBy} onChange={e => setGroupBy(e.target.value)}>
            <option value="hour">Hour</option><option value="day">Day</option>
          </select>
          <label>Schedule (5-field cron, empty = on demand)</label>
          <input value={cron} onChange={e => setCron(e.target.value)} placeholder="0 6 * * 1 (Mondays 6am)" />
          <label>Deliver to</label>
          <select value={channelId} onChange={e => setChannelId(e.target.value)}>
            <option value="">None (run history only)</option>
            {channels.map(c => <option key={c.id} value={c.id}>{c.type}: {c.target}</option>)}
          </select>
          <div style={{ marginTop: 14 }}><button type="submit">Create report</button></div>
        </form>
        {msg && <p className="muted">{msg}</p>}
      </div>
      {reports.map(r => (
        <div key={r.id} className="card" style={{ marginBottom: 10 }}>
          <b>{r.name}</b> <span className="muted">{r.definition.metrics.length} metrics, {r.definition.window_hours}h window, by {r.definition.group_by}</span>
          <div className="muted">
            {r.schedule_cron ? <>schedule {r.schedule_cron} - </> : 'on demand - '}
            {r.last_run_at ? `last run ${new Date(r.last_run_at).toLocaleString()}` : 'never run'}
          </div>
          <button onClick={() => runNow(r.id)}>Run now</button>
        </div>
      ))}
      {reports.length === 0 && <p className="muted">No reports yet.</p>}
    </>
  );
}
