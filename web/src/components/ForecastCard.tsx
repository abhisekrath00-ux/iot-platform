import { useEffect, useState } from 'react';
import { Line, XAxis, YAxis, Tooltip, ResponsiveContainer, CartesianGrid, Area, ComposedChart } from 'recharts';
import { chart as ct } from '../lib/theme';
import { formatValue } from '../lib/format';
import { api } from '../lib/api';

interface Fc {
  label: string; enough_data: boolean; reason?: string; useful?: boolean;
  forecast?: { t: string; value: number; lower: number; upper: number }[];
  backtest?: { mae: number; seasonal_naive_mae: number; holdout_hours: number; useful: boolean };
  change_points?: string[];
}
interface Rel { enough_data: boolean; hints: { name: string; r: number; lag: number }[]; caveat?: string }

// ForecastCard: statistical (not learned) forecast with its backtest and related signals.
// The forecast is only drawn as a prediction when the backtest beat repeating last season.
export default function ForecastCard({ device, point }: { device: string; point: string }) {
  const [fc, setFc] = useState<Fc | null>(null);
  const [rel, setRel] = useState<Rel | null>(null);
  useEffect(() => {
    setFc(null); setRel(null);
    const q = `device_id=${encodeURIComponent(device)}&point_id=${encodeURIComponent(point)}`;
    api<Fc>(`/v1/telemetry/forecast?${q}`).then(setFc).catch(() => setFc(null));
    api<Rel>(`/v1/telemetry/related?${q}`).then(setRel).catch(() => setRel(null));
  }, [device, point]);
  if (!fc) return null;
  const data = (fc.forecast ?? []).map(p => ({ t: new Date(p.t).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' }), v: p.value, band: [p.lower, p.upper] as [number, number] }));
  return (
    <div className="card" style={{ marginTop: 14 }} role="region" aria-label="Forecast">
      <b>Forecast (statistical, next 24h)</b>
      {!fc.enough_data
        ? <p className="muted">Not enough hourly history to forecast: {fc.reason ?? 'needs about 3 days with few gaps'}.</p>
        : !fc.useful
          ? <p className="muted">Not shown as a prediction: on the last 24h it did not beat repeating yesterday (error {fc.backtest?.mae} vs {fc.backtest?.seasonal_naive_mae}).</p>
          : <>
              <div style={{ height: 180 }}>
                <ResponsiveContainer>
                  <ComposedChart data={data}>
                    <CartesianGrid stroke={ct.grid} strokeDasharray="3 3" />
                    <XAxis dataKey="t" stroke={ct.axis} fontSize={11} />
                    <YAxis stroke={ct.axis} fontSize={11} domain={['auto', 'auto']} tickFormatter={formatValue} width={56} />
                    <Tooltip contentStyle={ct.tooltip} />
                    <Area dataKey="band" stroke="none" fill={ct.line} fillOpacity={0.15} />
                    <Line type="monotone" dataKey="v" stroke={ct.line} dot={false} strokeWidth={2} strokeDasharray="5 3" />
                  </ComposedChart>
                </ResponsiveContainer>
              </div>
              <p className="muted" style={{ fontSize: 12 }}>
                Shaded area is an approximate 95% interval. Backtest on the last {fc.backtest?.holdout_hours}h: error {fc.backtest?.mae} vs {fc.backtest?.seasonal_naive_mae} for repeating yesterday.
              </p>
            </>}
      {fc.change_points && fc.change_points.length > 0 && (
        <p className="muted" style={{ fontSize: 12 }}>Level shift detected (CUSUM) near {new Date(fc.change_points[0]).toLocaleString()}.</p>
      )}
      {rel && rel.enough_data && rel.hints.length > 0 && (
        <div style={{ marginTop: 8 }}>
          <b style={{ fontSize: 13 }}>Related signals on this device</b>
          <table style={{ marginTop: 4 }}><thead><tr><th>Point</th><th>r</th><th>Timing</th></tr></thead><tbody>
            {rel.hints.slice(0, 5).map(h => <tr key={h.name}><td>{h.name}</td><td>{h.r}</td><td>{h.lag > 0 ? `moves ${h.lag}h earlier` : h.lag < 0 ? `moves ${-h.lag}h later` : 'same time'}</td></tr>)}
          </tbody></table>
          <p className="muted" style={{ fontSize: 12 }}>{rel.caveat}. Hourly averages over 7 days.</p>
        </div>
      )}
    </div>
  );
}
