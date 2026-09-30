import { useRef } from 'react';
import { formatValue } from '../lib/format';
import { LatestPoint } from '../lib/api';

export interface Health { score: number; status: string; factors: { name: string; score: number; weight: number; detail: string }[]; }

const COLOR: Record<string, string> = { healthy: 'var(--ok)', degraded: 'var(--warn)', critical: 'var(--bad)', offline: 'var(--muted)' };
const R = 54, C = 2 * Math.PI * R;

// A lightweight digital twin: a tilting device "chassis" with live point
// readouts and status LED, next to an animated health ring with the factors
// that produced the score. Pure SVG/CSS, no external assets.
export default function DeviceTwin({ name, profile, health, points }: { name: string; profile?: string; health: Health | null; points: LatestPoint[] }) {
  const ref = useRef<HTMLDivElement>(null);
  const color = COLOR[health?.status ?? 'offline'];
  const tilt = (e: React.MouseEvent) => {
    const el = ref.current; if (!el) return;
    const b = el.getBoundingClientRect();
    const x = (e.clientX - b.left) / b.width - .5, y = (e.clientY - b.top) / b.height - .5;
    el.style.transform = `rotateY(${x * 14}deg) rotateX(${-y * 10}deg)`;
  };
  const reset = () => { if (ref.current) ref.current.style.transform = ''; };

  return (
    <div className="twin">
      <div className="twin-stage" onMouseMove={tilt} onMouseLeave={reset}>
        <div className="twin-device" ref={ref} aria-label={`Digital twin of ${name}`}>
          <div className="twin-head">
            <span className={`twin-led ${health?.status ?? 'offline'}`} style={{ background: color }} />
            <div><b>{name}</b><div className="muted" style={{ fontSize: 12 }}>{profile ?? 'device'}</div></div>
          </div>
          <div className="twin-readouts">
            {points.slice(0, 6).map(p => (
              <div key={p.point_id} className="twin-read">
                <span className="muted">{p.point_id}</span>
                <b>{formatValue(p.value)}<small> {p.unit}</small></b>
              </div>
            ))}
            {points.length === 0 && <span className="muted">No readings yet</span>}
          </div>
          <div className="twin-ports">{Array.from({ length: 8 }, (_, i) => <i key={i} />)}</div>
        </div>
      </div>
      <div className="twin-health">
        <svg viewBox="0 0 140 140" width="140" height="140" role="img" aria-label={`Health ${health?.score ?? 0} of 100`}>
          <circle cx="70" cy="70" r={R} fill="none" stroke="var(--line)" strokeWidth="12" />
          <circle className="twin-arc" cx="70" cy="70" r={R} fill="none" stroke={color} strokeWidth="12" strokeLinecap="round"
            strokeDasharray={C} strokeDashoffset={C * (1 - (health?.score ?? 0) / 100)} transform="rotate(-90 70 70)" />
          <text x="70" y="68" textAnchor="middle" fontSize="30" fontWeight="700" fill="var(--text)">{health?.score ?? '-'}</text>
          <text x="70" y="90" textAnchor="middle" fontSize="12" fill="var(--muted)">{health?.status ?? 'unknown'}</text>
        </svg>
        <div className="twin-factors">
          {(health?.factors ?? []).map(f => (
            <div key={f.name}>
              <div style={{ display: 'flex', justifyContent: 'space-between', fontSize: 13 }}><span>{f.name}</span><span className="muted">{f.score}</span></div>
              <div className="twin-bar"><div style={{ width: `${f.score}%`, background: f.score >= 80 ? 'var(--ok)' : f.score >= 50 ? 'var(--warn)' : 'var(--bad)' }} /></div>
              <div className="muted" style={{ fontSize: 12 }}>{f.detail}</div>
            </div>
          ))}
        </div>
      </div>
    </div>
  );
}
