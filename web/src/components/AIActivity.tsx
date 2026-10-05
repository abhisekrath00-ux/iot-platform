import { useEffect, useState } from 'react';
import { api } from '../lib/api';

interface Act {
  days: number; runs: number; failed_runs: number; cancelled_runs: number; tool_calls: number; refused_tool_calls: number;
  changes_proposed: number; changes_confirmed: number; changes_rejected: number; changes_autorun: number; pending_now: number;
  recent: { at: string; user: string; action: string; target?: string | null; detail: { model?: string; error?: string; cancelled?: boolean; tool_calls?: number; trace?: { tool: string; status: string; detail: string }[] } | null }[];
}

// What the AI did in this tenant, from the audit trail (admins only). Counts and the latest runs
// with each tool they used, including calls the platform refused.
export default function AIActivity() {
  const [a, setA] = useState<Act | null>(null);
  const [err, setErr] = useState('');
  useEffect(() => { api<Act>('/v1/ai/activity').then(setA).catch(e => setErr(String(e))); }, []);
  if (err) return null; // not an admin, or the AI layer is unused
  if (!a) return null;
  const stat: [string, number][] = [['Runs', a.runs], ['Failed', a.failed_runs], ['Tool calls', a.tool_calls], ['Refused by policy', a.refused_tool_calls],
    ['Changes proposed', a.changes_proposed], ['Confirmed', a.changes_confirmed], ['Rejected', a.changes_rejected], ['Waiting now', a.pending_now]];
  return (
    <div className="card" style={{ marginTop: 16 }}>
      <h2 style={{ marginTop: 0 }}>AI activity</h2>
      <p className="muted">Last {a.days} days, from the audit log. Every run lists the tools it used; the assistant acts with the signed-in user's permissions and cannot approve control commands.</p>
      <div style={{ display: 'flex', flexWrap: 'wrap', gap: 12, margin: '8px 0' }}>
        {stat.map(([k, v]) => <div key={k} style={{ minWidth: 110 }}><div style={{ fontSize: 22, fontWeight: 600 }}>{v}</div><div className="muted" style={{ fontSize: 12 }}>{k}</div></div>)}
      </div>
      {a.recent.length === 0 ? <p className="muted">No AI activity yet.</p> : (
        <table>
          <thead><tr><th>When</th><th>User</th><th>Event</th><th>Tools used</th></tr></thead>
          <tbody>
            {a.recent.slice(0, 20).map((r, i) => (
              <tr key={i}>
                <td>{new Date(r.at).toLocaleString()}</td>
                <td>{r.user || '-'}</td>
                <td>{r.action.replace('assistant.', '')}{r.detail?.error ? ` (failed: ${r.detail.error})` : r.detail?.cancelled ? ' (cancelled)' : ''}</td>
                <td>{(r.detail?.trace ?? []).map((t, j) => <span key={j} className={`pill ${t.status === 'ok' ? 'ok' : t.status === 'proposed' ? '' : 'warn'}`} title={t.detail} style={{ marginRight: 4 }}>{t.tool}</span>)}</td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
    </div>
  );
}
