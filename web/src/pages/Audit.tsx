import { useEffect, useState } from 'react';
import { api } from '../lib/api';

interface AuditRow { actor: string; action: string; target: string | null; detail: unknown; at: string; }

export default function Audit() {
  const [rows, setRows] = useState<AuditRow[]>([]);
  const [err, setErr] = useState('');

  useEffect(() => {
    api<AuditRow[]>('/v1/audit').then(setRows).catch(e => setErr(String(e)));
  }, []);

  return (
    <>
      <h1>Audit log</h1>
      {err && <p className="muted">{err} (admin role required)</p>}
      <table>
        <thead>
          <tr><th>Time</th><th>Actor</th><th>Action</th><th>Target</th><th>Detail</th></tr>
        </thead>
        <tbody>
          {rows.map((r, i) => (
            <tr key={i}>
              <td className="muted">{new Date(r.at).toLocaleString()}</td>
              <td>{r.actor}</td>
              <td>{r.action}</td>
              <td className="muted">{r.target ?? '-'}</td>
              <td className="muted" style={{ fontSize: 12, maxWidth: 320, overflow: 'hidden', textOverflow: 'ellipsis' }}>
                {r.detail ? JSON.stringify(r.detail) : '-'}
              </td>
            </tr>
          ))}
        </tbody>
      </table>
      {rows.length === 0 && !err && <p className="muted">No audit events yet.</p>}
    </>
  );
}
