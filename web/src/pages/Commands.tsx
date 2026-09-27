import { useEffect, useState } from 'react';
import { api, CommandRow } from '../lib/api';

// Control center. Every actuation is request -> approve (four-eyes) -> send
// -> ack -> measured outcome, all audited. See docs/security.md.
export default function Commands() {
  const [rows, setRows] = useState<CommandRow[]>([]);
  const [err, setErr] = useState('');
  const load = () => api<CommandRow[]>('/v1/commands').then(setRows).catch(e => setErr(String(e)));
  useEffect(() => { load(); }, []);
  const approve = (id: string) =>
    api(`/v1/commands/${id}/approve`, { method: 'POST' }).then(load).catch(e => setErr(String(e)));
  return (
    <>
      <h1>Control</h1>
      {err && <p className="muted">{err}</p>}
      <table>
        <thead><tr><th>Device</th><th>Action</th><th>Status</th><th>Requested by</th><th></th></tr></thead>
        <tbody>
          {rows.map(c => (
            <tr key={c.request_id}>
              <td>{c.device_id}</td><td>{c.action}</td>
              <td><span className={`pill ${c.status === 'approved' ? 'ok' : c.status.startsWith('pending') ? 'warn' : ''}`}>{c.status}</span></td>
              <td className="muted">{c.requested_by}</td>
              <td>{c.status === 'pending_approval' && <button className="ghost" onClick={() => approve(c.request_id)}>Approve</button>}</td>
            </tr>
          ))}
        </tbody>
      </table>
    </>
  );
}
