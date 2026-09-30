import { chart as ct } from '../lib/theme';
import { formatValue } from '../lib/format';
import { useEffect, useMemo, useState } from 'react';
import { LineChart, Line, XAxis, YAxis, Tooltip, ResponsiveContainer, CartesianGrid } from 'recharts';
import { api, Device, LatestPoint } from '../lib/api';
import { Widget, WidgetType, thresholdState, optNum } from '../lib/widgets';
import { GaugeWidget, BarWidget, StatusWidget } from '../components/Widgets';

interface Dashboard { id: string; name: string; layout: { widgets?: Widget[] }; }

const TEMPLATES = [
  { id: 'blank', name: 'Blank', desc: 'Empty canvas. Add widgets yourself.' },
  { id: 'kpi-grid', name: 'KPI grid', desc: 'A live value card for every point on one device.' },
  { id: 'trends', name: 'Trends', desc: 'A 24h line chart per point (up to 4) on one device.' },
];

const uid = () => Math.random().toString(36).slice(2, 10);

function KpiWidget({ w }: { w: Widget }) {
  const [pt, setPt] = useState<LatestPoint | null>(null);
  const [err, setErr] = useState('');
  useEffect(() => {
    const load = () => api<LatestPoint[]>(`/v1/telemetry/latest?device_id=${w.device_id}`)
      .then(d => setPt(d.find(p => p.point_id === w.point_id) ?? null))
      .catch(e => setErr(String(e)));
    load();
    const t = setInterval(load, 10000);
    return () => clearInterval(t);
  }, [w.device_id, w.point_id]);
  return (
    <div className="card">
      <div className="muted">{w.title}</div>
      {err && <div className="muted" style={{ fontSize: 12 }}>{err}</div>}
      {pt ? (
        <>
          <div className="kpi" style={{ color: ({ ok: 'inherit', warn: 'var(--warn, #b25000)', bad: 'var(--bad, #d70015)', none: 'inherit' } as Record<string, string>)[thresholdState(pt.value, w.warn, w.crit)] }}>{formatValue(pt.value)} <small>{pt.unit}</small></div>
          <span className={`pill ${pt.quality === 'measured' ? 'ok' : 'warn'}`}>{pt.quality}</span>
          <div className="muted" style={{ fontSize: 12 }}>{new Date(pt.observed_at).toLocaleString()}</div>
        </>
      ) : !err && <div className="muted">no data yet</div>}
    </div>
  );
}

function SeriesWidget({ w }: { w: Widget }) {
  const [series, setSeries] = useState<{ t: string; v: number }[]>([]);
  const [err, setErr] = useState('');
  useEffect(() => {
    if (!w.point_id) return;
    api<{ t: string; v: number }[]>(`/v1/telemetry/series?device_id=${w.device_id}&point_id=${w.point_id}`)
      .then(d => setSeries(d.map(p => ({ t: new Date(p.t).toLocaleTimeString(), v: p.v }))))
      .catch(e => setErr(String(e)));
  }, [w.device_id, w.point_id]);
  return (
    <div className="card">
      <div className="muted">{w.title}</div>
      {err && <div className="muted" style={{ fontSize: 12 }}>{err}</div>}
      <div style={{ width: '100%', height: 220 }}>
        <ResponsiveContainer>
          <LineChart data={series}>
            <CartesianGrid stroke={ct.grid} />
            <XAxis dataKey="t" stroke={ct.axis} fontSize={11} />
            <YAxis stroke={ct.axis} fontSize={11} domain={['auto', 'auto']} tickFormatter={formatValue} width={56} />
            <Tooltip contentStyle={ct.tooltip} />
            <Line type="monotone" dataKey="v" stroke={ct.line} dot={false} strokeWidth={2} />
          </LineChart>
        </ResponsiveContainer>
      </div>
    </div>
  );
}

export default function Dashboards() {
  const [boards, setBoards] = useState<Dashboard[]>([]);
  const [devices, setDevices] = useState<Device[]>([]);
  const [sel, setSel] = useState<Dashboard | null>(null);
  const [err, setErr] = useState('');
  const [editing, setEditing] = useState(false);
  const [name, setName] = useState('');
  // create form
  const [newName, setNewName] = useState('');
  const [tpl, setTpl] = useState('blank');
  const [tplDevice, setTplDevice] = useState('');
  // widget form
  const [wType, setWType] = useState<WidgetType>('kpi');
  const [wMin, setWMin] = useState('');
  const [wMax, setWMax] = useState('');
  const [wWarn, setWWarn] = useState('');
  const [wCrit, setWCrit] = useState('');
  const [wTitle, setWTitle] = useState('');
  const [wDevice, setWDevice] = useState('');
  const [wPoint, setWPoint] = useState('');
  const [devicePoints, setDevicePoints] = useState<LatestPoint[]>([]);

  const deviceName = useMemo(() => {
    const m = new Map(devices.map(d => [d.id, d.name || d.id]));
    return (id: string) => m.get(id) ?? id;
  }, [devices]);

  const loadBoards = () => api<Dashboard[]>('/v1/dashboards').then(d => {
    setBoards(d);
    if (sel) setSel(d.find(b => b.id === sel.id) ?? null);
  }).catch(e => setErr(String(e)));

  useEffect(() => {
    loadBoards();
    api<Device[]>('/v1/devices').then(d => { setDevices(d); if (d.length) { setTplDevice(d[0].id); setWDevice(d[0].id); } }).catch(() => {});
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  useEffect(() => {
    if (!wDevice) { setDevicePoints([]); return; }
    api<LatestPoint[]>(`/v1/telemetry/latest?device_id=${wDevice}`)
      .then(d => { setDevicePoints(d); if (d.length) setWPoint(d[0].point_id); })
      .catch(() => setDevicePoints([]));
  }, [wDevice]);

  const createBoard = async () => {
    if (!newName.trim()) return;
    let widgets: Widget[] = [];
    if (tpl !== 'blank' && tplDevice) {
      const pts = await api<LatestPoint[]>(`/v1/telemetry/latest?device_id=${tplDevice}`).catch(() => [] as LatestPoint[]);
      if (tpl === 'kpi-grid') {
        widgets = pts.map(p => ({ id: uid(), type: 'kpi', title: `${deviceName(tplDevice)} ${p.point_id}`, device_id: tplDevice, point_id: p.point_id }));
      } else {
        widgets = pts.slice(0, 4).map(p => ({ id: uid(), type: 'timeseries', title: `${deviceName(tplDevice)} ${p.point_id} (24h)`, device_id: tplDevice, point_id: p.point_id }));
      }
    }
    const r = await api<{ id: string }>('/v1/dashboards', { method: 'POST', body: JSON.stringify({ name: newName.trim(), layout: { widgets } }) }).catch(e => { setErr(String(e)); return null; });
    if (!r) return;
    setNewName('');
    loadBoards();
  };

  const saveBoard = async () => {
    if (!sel) return;
    await api(`/v1/dashboards/${sel.id}`, { method: 'PUT', body: JSON.stringify({ name: name || sel.name, layout: { widgets: sel.layout.widgets ?? [] } }) }).catch(e => setErr(String(e)));
    setEditing(false);
    loadBoards();
  };

  const deleteBoard = async () => {
    if (!sel || !window.confirm(`Delete dashboard "${sel.name}"?`)) return;
    await api(`/v1/dashboards/${sel.id}`, { method: 'DELETE' }).catch(e => setErr(String(e)));
    setSel(null);
    setEditing(false);
    loadBoards();
  };

  const addWidget = () => {
    if (!sel || !wDevice) return;
    const w: Widget = {
      id: uid(), type: wType, device_id: wDevice, point_id: wPoint || undefined,
      title: wTitle.trim() || `${deviceName(wDevice)} ${wType === 'bar' || wType === 'status' ? '' : wPoint || ''}${wType === 'timeseries' ? ' (24h)' : ''}`.trim(),
      min: optNum(wMin), max: optNum(wMax), warn: optNum(wWarn), crit: optNum(wCrit),
    };
    setSel({ ...sel, layout: { widgets: [...(sel.layout.widgets ?? []), w] } });
    setWTitle('');
  };

  const removeWidget = (id: string) => {
    if (!sel) return;
    setSel({ ...sel, layout: { widgets: (sel.layout.widgets ?? []).filter(w => w.id !== id) } });
  };

  const widgets = sel?.layout.widgets ?? [];

  return (
    <>
      <h1>Dashboards</h1>
      {err && <p className="muted">{err}</p>}

      <div className="card" style={{ marginBottom: 16 }}>
        <div className="muted" style={{ marginBottom: 8 }}>New dashboard</div>
        <div style={{ display: 'flex', gap: 8, flexWrap: 'wrap', alignItems: 'end' }}>
          <div><label>Name</label><input value={newName} onChange={e => setNewName(e.target.value)} placeholder="Plant overview" /></div>
          <div><label>Template</label>
            <select value={tpl} onChange={e => setTpl(e.target.value)}>
              {TEMPLATES.map(t => <option key={t.id} value={t.id}>{t.name}</option>)}
            </select>
          </div>
          {tpl !== 'blank' && (
            <div><label>Device</label>
              <select value={tplDevice} onChange={e => setTplDevice(e.target.value)}>
                {devices.map(d => <option key={d.id} value={d.id}>{d.name || d.id}</option>)}
              </select>
            </div>
          )}
          <button onClick={createBoard} disabled={!newName.trim()}>Create</button>
        </div>
        <div className="muted" style={{ fontSize: 12, marginTop: 6 }}>{TEMPLATES.find(t => t.id === tpl)?.desc}</div>
      </div>

      <div style={{ display: 'flex', gap: 8, flexWrap: 'wrap', marginBottom: 16 }}>
        {boards.map(b => (
          <button key={b.id} className={sel?.id === b.id ? '' : 'ghost'} onClick={() => { setSel(b); setName(b.name); setEditing(false); }}>{b.name}</button>
        ))}
        {boards.length === 0 && <span className="muted">No dashboards yet.</span>}
      </div>

      {sel && (
        <>
          <div style={{ display: 'flex', gap: 8, alignItems: 'center', marginBottom: 12 }}>
            {editing ? <input value={name} onChange={e => setName(e.target.value)} /> : <h2 style={{ margin: 0 }}>{sel.name}</h2>}
            {editing ? (
              <>
                <button onClick={saveBoard}>Save</button>
                <button className="ghost" onClick={() => { setEditing(false); loadBoards(); }}>Cancel</button>
                <button className="ghost" onClick={deleteBoard}>Delete</button>
              </>
            ) : (
              <button className="ghost" onClick={() => setEditing(true)}>Edit</button>
            )}
          </div>

          {editing && (
            <div className="card" style={{ marginBottom: 16 }}>
              <div className="muted" style={{ marginBottom: 8 }}>Add widget</div>
              <div style={{ display: 'flex', gap: 8, flexWrap: 'wrap', alignItems: 'end' }}>
                <div><label>Type</label>
                  <select value={wType} onChange={e => setWType(e.target.value as WidgetType)}>
                    <option value="kpi">Live value (KPI)</option>
                    <option value="gauge">Gauge</option>
                    <option value="timeseries">Trend (24h chart)</option>
                    <option value="bar">Bars (all points)</option>
                    <option value="status">Device status</option>
                  </select>
                </div>
                <div><label>Device</label>
                  <select value={wDevice} onChange={e => setWDevice(e.target.value)}>
                    {devices.map(d => <option key={d.id} value={d.id}>{d.name || d.id}</option>)}
                  </select>
                </div>
                <div><label>Point</label>
                  <select value={wPoint} onChange={e => setWPoint(e.target.value)}>
                    {devicePoints.map(p => <option key={p.point_id} value={p.point_id}>{p.point_id}</option>)}
                    {devicePoints.length === 0 && <option value="">no live points</option>}
                  </select>
                </div>
                <div><label>Title (optional)</label><input value={wTitle} onChange={e => setWTitle(e.target.value)} /></div>
                {(wType === 'gauge' || wType === 'bar') && <div><label>Min</label><input type="number" style={{ width: 80 }} value={wMin} onChange={e => setWMin(e.target.value)} /></div>}
                {(wType === 'gauge' || wType === 'bar') && <div><label>Max</label><input type="number" style={{ width: 80 }} value={wMax} onChange={e => setWMax(e.target.value)} /></div>}
                {wType !== 'timeseries' && wType !== 'status' && <div><label>Warn at</label><input type="number" style={{ width: 80 }} value={wWarn} onChange={e => setWWarn(e.target.value)} /></div>}
                {wType !== 'timeseries' && wType !== 'status' && <div><label>Critical at</label><input type="number" style={{ width: 80 }} value={wCrit} onChange={e => setWCrit(e.target.value)} /></div>}
                <button onClick={addWidget} disabled={!wDevice || (!wPoint && wType !== 'bar' && wType !== 'status')}>Add</button>
              </div>
            </div>
          )}

          <div className="cards">
            {widgets.map(w => (
              <div key={w.id} style={{ position: 'relative', minWidth: 0, gridColumn: w.type === 'bar' || w.type === 'timeseries' ? 'span 2' : undefined }}>
                {w.type === 'kpi' ? <KpiWidget w={w} /> : w.type === 'gauge' ? <GaugeWidget w={w} /> : w.type === 'bar' ? <BarWidget w={w} /> : w.type === 'status' ? <StatusWidget w={w} /> : <SeriesWidget w={w} />}
                {editing && <button className="ghost" style={{ position: 'absolute', top: 6, right: 6 }} onClick={() => removeWidget(w.id)}>x</button>}
              </div>
            ))}
            {widgets.length === 0 && <p className="muted">No widgets. {editing ? 'Add one above.' : 'Click Edit to add widgets.'}</p>}
          </div>
        </>
      )}
    </>
  );
}
