import { useEffect, useState } from 'react';
import { api } from '../lib/api';

interface Usage { quotas: { resource: string; used: number; limit: number | null }[]; points_24h: number; points_30d: number; note: string; }
const LABEL: Record<string, string> = { devices: 'Devices', users: 'Users and pending invites', api_keys: 'Active API keys', customers: 'Customers' };

// Admin-only. Shows use against the limits the platform operator set. Nothing here is billed.
export default function UsageCard() {
  const [u, setU] = useState<Usage | null>(null);
  useEffect(() => { api<Usage>('/v1/usage').then(setU).catch(() => setU(null)); }, []);
  if (!u) return null;
  return (
    <div className="card" style={{ marginBottom: 16 }}>
      <b>Usage</b>
      <table style={{ marginTop: 8 }}>
        <tbody>
          {u.quotas.map(q => (
            <tr key={q.resource}>
              <td>{LABEL[q.resource] ?? q.resource}</td>
              <td>{q.used}{q.limit === null ? '' : ` of ${q.limit}`}</td>
              <td className="muted">{q.limit === null ? 'no limit' : q.used >= q.limit ? 'limit reached' : ''}</td>
            </tr>
          ))}
          <tr><td>Data points, last 24 hours</td><td>{u.points_24h.toLocaleString()}</td><td /></tr>
          <tr><td>Data points, last 30 days</td><td>{u.points_30d.toLocaleString()}</td><td /></tr>
        </tbody>
      </table>
      <div className="muted" style={{ fontSize: 12, marginTop: 6 }}>{u.note}</div>
    </div>
  );
}
