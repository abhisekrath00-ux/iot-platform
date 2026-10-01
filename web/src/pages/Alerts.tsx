import { useEffect, useState } from 'react';
import { api, AlertRow, AlertDetail } from '../lib/api';
import Empty from '../components/Empty';

const FILTERS = ['open', 'acknowledged', 'resolved', 'all'] as const;
type Filter = typeof FILTERS[number];

export default function Alerts() {
  const [alerts, setAlerts] = useState<AlertRow[]>([]);
  const [filter, setFilter] = useState<Filter>('open');
  const [err, setErr] = useState('');
  const [assets, setAssets] = useState<{ id: string; name: string }[]>([]);
  const [asset, setAsset] = useState('');
  useEffect(() => { api<typeof assets>('/v1/assets').then(setAssets).catch(() => {}); }, []);
  const [sel, setSel] = useState<AlertDetail | null>(null);
  const [note, setNote] = useState('');

  const load = () => api<AlertRow[]>(`/v1/alerts?${new URLSearchParams({ ...(filter === 'all' ? {} : { status: filter }), ...(asset ? { asset_id: asset } : {}) })}`)
    .then(a => { setAlerts(a); setErr(''); }).catch(e => setErr(String(e)));
  useEffect(() => { load(); /* eslint-disable-next-line react-hooks/exhaustive-deps */ }, [filter, asset]);

  const open = (id: string) => api<AlertDetail>(`/v1/alerts/${id}`).then(setSel).catch(e => setErr(String(e)));
  const act = (id: string, action: 'ack' | 'resolve') =>
    api(`/v1/alerts/${id}/${action}`, { method: 'POST' })
      .then(() => Promise.all([load(), sel?.id === id ? open(id) : undefined]))
      .catch(e => setErr(`${action} failed: ${e}`));
  const comment = () => {
    if (!sel || !note.trim()) return;
    api(`/v1/alerts/${sel.id}/comments`, { method: 'POST', body: JSON.stringify({ body: note }) })
      .then(() => { setNote(''); return open(sel.id); }).catch(e => setErr(String(e)));
  };

  return (
    <>
      <h1>Alerts</h1>
      <div style={{ display: 'flex', gap: 8, marginBottom: 16 }}>
        {assets.length > 0 && <select aria-label="Filter by asset" value={asset} onChange={e => setAsset(e.target.value)} style={{ width: 'auto' }}><option value="">All assets</option>{assets.map(a => <option key={a.id} value={a.id}>{a.name}</option>)}</select>}
        {FILTERS.map(f => <button key={f} className={filter === f ? '' : 'ghost'} onClick={() => setFilter(f)}>{f}</button>)}
      </div>
      {err && <p className="muted">{err}</p>}
      {alerts.length === 0 && !err ? (
        <Empty title={filter === 'open' ? 'All clear' : `No ${filter} alerts`} hint="Create rules and flows to be notified when something drifts." />
      ) : (
        <table>
          <thead><tr><th>Severity</th><th>Message</th><th>Status</th><th>When</th><th></th></tr></thead>
          <tbody>
            {alerts.map(a => (
              <tr key={a.id} style={{ cursor: 'pointer' }} onClick={() => open(a.id)}>
                <td><span className={`pill ${a.severity === 'critical' ? 'bad' : a.severity === 'warning' ? 'warn' : 'ok'}`}>{a.severity}</span></td>
                <td>{a.message}</td>
                <td className="muted">{a.status}{a.acknowledged_by && a.status === 'acknowledged' ? ` by ${a.acknowledged_by}` : ''}</td>
                <td className="muted">{new Date(a.created_at).toLocaleString()}</td>
                <td onClick={e => e.stopPropagation()} style={{ whiteSpace: 'nowrap' }}>
                  {a.status === 'open' && <button className="ghost" onClick={() => act(a.id, 'ack')}>Acknowledge</button>}{' '}
                  {a.status !== 'resolved' && <button className="ghost" onClick={() => act(a.id, 'resolve')}>Resolve</button>}
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      )}

      {sel && (
        <div className="card" style={{ marginTop: 16 }}>
          <div style={{ display: 'flex', justifyContent: 'space-between' }}>
            <h2 style={{ margin: 0 }}>{sel.message}</h2>
            <button className="ghost" onClick={() => setSel(null)}>Close</button>
          </div>
          <p className="muted" style={{ marginTop: 6 }}>
            {sel.severity} · {sel.status} · raised {new Date(sel.created_at).toLocaleString()}
            {sel.acknowledged_by && ` · acknowledged by ${sel.acknowledged_by}`}
            {sel.resolved_by && ` · resolved by ${sel.resolved_by}`}
          </p>
          <div className="muted" style={{ marginBottom: 6 }}>Notes</div>
          {sel.comments.length === 0 && <p className="muted">No notes yet.</p>}
          {sel.comments.map((c, i) => (
            <div key={i} style={{ padding: '6px 0', borderTop: '1px solid var(--line)' }}>
              <b>{c.author}</b> <span className="muted">{new Date(c.created_at).toLocaleString()}</span>
              <div>{c.body}</div>
            </div>
          ))}
          <div style={{ display: 'flex', gap: 8, marginTop: 10 }}>
            <input style={{ flex: 1 }} value={note} maxLength={2000} placeholder="Add a note for the next shift" onChange={e => setNote(e.target.value)} />
            <button onClick={comment} disabled={!note.trim()}>Add note</button>
          </div>
        </div>
      )}
    </>
  );
}
