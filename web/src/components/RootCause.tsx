import { useState } from 'react';
import { api } from '../lib/api';

interface Hint { device_id: string; point_id: string; r: number; lag_hours: number; reading: string }
interface Result { label: string; caveat: string; method: string; point_id: string; window_from: string; window_to: string; enough_data: boolean; reason?: string; examined: number; hints: Hint[] }

/** Points that move with the alerting point. Statistical, on request, never a claim of cause. */
export default function RootCause({ alertId }: { alertId: string }) {
  const [res, setRes] = useState<Result | null>(null);
  const [err, setErr] = useState('');
  const [busy, setBusy] = useState(false);
  const run = () => {
    setBusy(true); setErr('');
    api<Result>(`/v1/alerts/${encodeURIComponent(alertId)}/root-cause`).then(setRes).catch(e => setErr(String(e.message ?? e).replace(/^\d+:\s*/, ''))).finally(() => setBusy(false));
  };
  return (
    <div style={{ marginTop: 14, paddingTop: 10, borderTop: '1px solid var(--line)' }}>
      <div style={{ display: 'flex', alignItems: 'center', gap: 10 }}>
        <b>What moved with this?</b>
        <button className="ghost" onClick={run} disabled={busy}>{busy ? 'Checking...' : res ? 'Run again' : 'Check related signals'}</button>
      </div>
      {err && <p className="muted" role="alert">{err}</p>}
      {res && (
        <div style={{ marginTop: 8 }}>
          <p className="muted" style={{ margin: '0 0 6px' }}>
            <span className="pill ok">{res.label}</span> {res.caveat}. Window {new Date(res.window_from).toLocaleString()} to {new Date(res.window_to).toLocaleString()}; {res.examined} other series compared against {res.point_id}.
          </p>
          {!res.enough_data && <p className="muted">Not enough data: {res.reason}.</p>}
          {res.enough_data && res.hints.length === 0 && <p className="muted">Nothing correlated strongly (|r| of 0.6 or more).</p>}
          {res.hints.map(h => <div key={h.device_id + h.point_id} style={{ padding: '4px 0' }}>{h.reading}</div>)}
        </div>
      )}
    </div>
  );
}
