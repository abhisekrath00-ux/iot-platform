import { useCallback, useEffect, useState } from 'react';
import { api } from '../lib/api';
import LineChart, { Series } from '../components/LineChart';

interface Health {
  uptime_seconds: number; go: string; errors_24h: number; warnings_24h: number; last_error_at: string;
  current: Record<string, number>;
  retention: { metric_days: number; log_days: number; log_rows: number; sample_seconds: number };
  sources: { name: string; available: boolean; note?: string }[];
}
const NAMES = ['api_requests_per_min', 'api_errors_per_min', 'api_p50_ms', 'api_p95_ms', 'ingest_rows_per_min', 'db_size_mb', 'db_connections', 'db_ping_ms', 'alerts_open', 'alerts_acknowledged', 'api_heap_mb', 'api_goroutines'];
const RANGES: [string, number][] = [['15 min', 15], ['1 hour', 60], ['6 hours', 360], ['24 hours', 1440], ['7 days', 10080]];

function Stat({ label, value, tone }: { label: string; value: string; tone?: 'ok' | 'warn' | 'bad' }) {
  const c = tone === 'bad' ? '#dc2626' : tone === 'warn' ? '#d97706' : tone === 'ok' ? '#16a34a' : undefined;
  return <div className="card" style={{ minWidth: 130 }}><div className="muted">{label}</div><div style={{ fontSize: 24, fontWeight: 600, color: c }}>{value}</div></div>;
}

export default function SystemHealth() {
  const [h, setH] = useState<Health | null>(null);
  const [series, setSeries] = useState<Record<string, [number, number][]>>({});
  const [mins, setMins] = useState(60);
  const [err, setErr] = useState('');
  const load = useCallback(() => {
    api<Health>('/v1/system/health').then((x) => { setH(x); setErr(''); }).catch((e) => setErr(String(e.message ?? e)));
    api<{ series: Record<string, [number, number][]> }>(`/v1/system/metrics?names=${NAMES.join(',')}&minutes=${mins}`).then((x) => setSeries(x.series ?? {})).catch(() => {});
  }, [mins]);
  useEffect(() => { load(); const t = setInterval(load, 15000); return () => clearInterval(t); }, [load]);
  const s = (n: string, name: string, color: string): Series => ({ name, color, points: series[n] ?? [] });
  if (err) return <div><h2>System health</h2><p role="alert">{err.includes('403') ? 'Admins only.' : err}</p></div>;
  if (!h) return <div><h2>System health</h2><p className="muted">Loading…</p></div>;
  const c = h.current;
  const up = h.uptime_seconds;
  return (
    <div>
      <h2>System health</h2>
      <p className="muted">Built-in metrics and logs, no Prometheus or Grafana needed. Refreshes every 15 s.</p>
      <div style={{ display: 'flex', gap: 12, flexWrap: 'wrap', marginBottom: 12 }}>
        <Stat label="API" value={c.db_ping_ms !== undefined ? 'up' : 'unknown'} tone="ok" />
        <Stat label="Database ping" value={c.db_ping_ms !== undefined ? `${c.db_ping_ms.toFixed(1)} ms` : 'n/a'} tone={c.db_ping_ms > 100 ? 'warn' : 'ok'} />
        <Stat label="Uptime" value={up > 86400 ? `${Math.floor(up / 86400)} d` : up > 3600 ? `${Math.floor(up / 3600)} h` : `${Math.floor(up / 60)} min`} />
        <Stat label="Errors (24 h)" value={String(h.errors_24h)} tone={h.errors_24h ? 'bad' : 'ok'} />
        <Stat label="Warnings (24 h)" value={String(h.warnings_24h)} tone={h.warnings_24h ? 'warn' : 'ok'} />
        <Stat label="Open alerts" value={String(c.alerts_open ?? 0)} tone={c.alerts_open ? 'warn' : 'ok'} />
        <Stat label="Database size" value={c.db_size_mb !== undefined ? `${c.db_size_mb.toFixed(0)} MB` : 'n/a'} />
        <Stat label="API memory" value={c.api_heap_mb !== undefined ? `${c.api_heap_mb.toFixed(0)} MB` : 'n/a'} />
      </div>
      <div style={{ marginBottom: 12 }}>
        {RANGES.map(([l, m]) => <button key={m} style={{ marginRight: 6, opacity: m === mins ? 1 : 0.55 }} onClick={() => setMins(m)}>{l}</button>)}
        <a href="/dev-tools" style={{ marginLeft: 12 }}>Dev tools and logs →</a>
      </div>
      <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fill,minmax(340px,1fr))', gap: 12 }}>
        <LineChart title="API requests per minute" series={[s('api_requests_per_min', 'requests', '#2563eb'), s('api_errors_per_min', '5xx errors', '#dc2626')]} />
        <LineChart title="API latency" unit="ms" series={[s('api_p50_ms', 'p50', '#16a34a'), s('api_p95_ms', 'p95', '#d97706')]} />
        <LineChart title="Telemetry ingest (rows per minute)" series={[s('ingest_rows_per_min', 'rows/min', '#7c3aed')]} />
        <LineChart title="Database size" unit="MB" series={[s('db_size_mb', 'size', '#0891b2')]} />
        <LineChart title="Database connections and ping" series={[s('db_connections', 'connections', '#0891b2'), s('db_ping_ms', 'ping ms', '#d97706')]} />
        <LineChart title="Alerts" series={[s('alerts_open', 'open', '#dc2626'), s('alerts_acknowledged', 'acknowledged', '#d97706')]} />
        <LineChart title="API process memory" unit="MB" series={[s('api_heap_mb', 'heap', '#2563eb')]} />
        <LineChart title="Goroutines" series={[s('api_goroutines', 'goroutines', '#64748b')]} />
      </div>
      <div className="card" style={{ marginTop: 12 }}>
        <strong>What is measured</strong>
        <ul style={{ margin: '8px 0 0', paddingLeft: 18 }}>
          {h.sources.map((x) => <li key={x.name}>{x.available ? '✔' : '✘ not available:'} {x.name}{x.note ? ` (${x.note})` : ''}</li>)}
        </ul>
        <p className="muted">Sampled every {h.retention.sample_seconds} s, kept {h.retention.metric_days} days. Logs kept {h.retention.log_days} days (max {h.retention.log_rows} rows). {h.go}.</p>
      </div>
    </div>
  );
}
