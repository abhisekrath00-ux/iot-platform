export interface Series { name: string; color: string; points: [number, number][]; }

/** Small dependency-free SVG line chart. Shows "no data yet" instead of a flat fake line. */
export default function LineChart({ title, unit, series, height = 120 }: { title: string; unit?: string; series: Series[]; height?: number }) {
  const W = 360, pad = 4;
  const all = series.flatMap((s) => s.points);
  const has = all.length > 1;
  const t0 = has ? Math.min(...all.map((p) => p[0])) : 0, t1 = has ? Math.max(...all.map((p) => p[0])) : 1;
  const vmax = has ? Math.max(1e-9, ...all.map((p) => p[1])) : 1;
  const x = (t: number) => pad + ((t - t0) / Math.max(1, t1 - t0)) * (W - 2 * pad);
  const y = (v: number) => height - pad - (v / vmax) * (height - 2 * pad);
  const last = (s: Series) => (s.points.length ? s.points[s.points.length - 1][1] : null);
  return (
    <div className="card" style={{ minWidth: 0 }}>
      <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'baseline' }}>
        <strong>{title}</strong>
        <span className="muted">{has ? `max ${vmax.toFixed(vmax < 10 ? 2 : 0)}${unit ? ' ' + unit : ''}` : ''}</span>
      </div>
      <svg viewBox={`0 0 ${W} ${height}`} role="img" aria-label={title} style={{ width: '100%', height }}>
        <line x1={pad} x2={W - pad} y1={height - pad} y2={height - pad} stroke="currentColor" opacity={0.2} />
        {has ? series.map((s) => s.points.length > 1 && (
          <polyline key={s.name} fill="none" stroke={s.color} strokeWidth={2} points={s.points.map((p) => `${x(p[0]).toFixed(1)},${y(p[1]).toFixed(1)}`).join(' ')} />
        )) : <text x={W / 2} y={height / 2} textAnchor="middle" fill="currentColor" opacity={0.6} fontSize={13}>no data yet (sampled every 60 s)</text>}
      </svg>
      <div style={{ display: 'flex', gap: 12, flexWrap: 'wrap', fontSize: 12 }}>
        {series.map((s) => <span key={s.name}><span style={{ color: s.color }}>●</span> {s.name}{last(s) !== null ? `: ${last(s)!.toFixed(2)}${unit ? ' ' + unit : ''}` : ''}</span>)}
      </div>
    </div>
  );
}
