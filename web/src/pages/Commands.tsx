import { useEffect, useState } from 'react';
import { api, CommandRow } from '../lib/api';
import Empty from '../components/Empty';
import ControlTargets from '../components/ControlTargets';

// Control center. Every actuation is request -> approve (four-eyes) -> send
// -> ack -> measured outcome, all audited. See docs/security.md.
export default function Commands() {
  const [rows, setRows] = useState<CommandRow[]>([]);
  const [err, setErr] = useState('');
  const load = () => api<CommandRow[]>('/v1/commands').then(setRows).catch(e => setErr(String(e)));
  useEffect(() => { load(); }, []);
  const approve = async (id: string) => {
    setErr('');
    let body: string | undefined;
    const t = await api<{ required_for_approval: boolean }>('/v1/me/totp').catch(() => null);
    if (t?.required_for_approval) {
      const code = window.prompt('Authenticator code (6 digits)');
      if (!code) return;
      body = JSON.stringify({ code });
    }
    api(`/v1/commands/${id}/approve`, { method: 'POST', body }).then(load).catch(e => setErr(String(e)));
  };
  return (
    <>
      <h1>Control</h1>
      {err && <p className="muted">{err}</p>}
      {rows.length === 0 && !err && (
        <Empty title="No control requests" hint="Actuation requests appear here for four-eyes approval. Nothing can switch a physical output without an approved, audited request." />
      )}
      {rows.length > 0 && <table>
        <thead><tr><th>Device</th><th>Action</th><th>What and why</th><th>Status</th><th>Requested by</th><th></th></tr></thead>
        <tbody>
          {rows.map(c => (
            <tr key={c.request_id}>
              <td>{c.device_id}</td><td>{c.action}</td>
              <td className="muted">{c.parameters && Object.keys(c.parameters).length > 0 ? `set ${String(c.parameters.point ?? '')} to ${String(c.parameters.value ?? '')}` : ''}{c.reason ? ` · ${c.reason}` : ''}</td>
              <td><span className={`pill ${c.status === 'approved' ? 'ok' : c.status.startsWith('pending') ? 'warn' : ''}`}>{c.status}</span></td>
              <td className="muted">{c.requested_by}</td>
              <td>{c.status === 'pending_approval' && <button className="ghost" onClick={() => approve(c.request_id)}>Approve</button>}</td>
            </tr>
          ))}
        </tbody>
      </table>}
      <ControlTargets />
    </>
  );
}
