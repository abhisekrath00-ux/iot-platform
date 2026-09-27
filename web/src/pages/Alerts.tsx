import { useEffect, useState } from 'react';
import { api, AlertRow } from '../lib/api';

export default function Alerts() {
  const [alerts, setAlerts] = useState<AlertRow[]>([]);
  const [err, setErr] = useState('');
  useEffect(() => { api<AlertRow[]>('/v1/alerts').then(setAlerts).catch(e => setErr(String(e))); }, []);
  return (
    <>
      <h1>Alerts</h1>
      {err && <p className="muted">{err}</p>}
      <table>
        <thead><tr><th>Severity</th><th>Message</th><th>Status</th><th>When</th></tr></thead>
        <tbody>
          {alerts.map(a => (
            <tr key={a.id}>
              <td><span className={`pill ${a.severity === 'critical' ? 'bad' : a.severity === 'warning' ? 'warn' : 'ok'}`}>{a.severity}</span></td>
              <td>{a.message}</td><td className="muted">{a.status}</td>
              <td className="muted">{new Date(a.created_at).toLocaleString()}</td>
            </tr>
          ))}
        </tbody>
      </table>
    </>
  );
}
