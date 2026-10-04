import { chart as ct } from '../lib/theme';
import { formatValue } from '../lib/format';
import { useEffect, useMemo, useState } from 'react';
import ShareWithCustomer from '../components/ShareWithCustomer';
import { LineChart, Line, XAxis, YAxis, Tooltip, ResponsiveContainer, CartesianGrid } from 'recharts';
import { api, Device, LatestPoint } from '../lib/api';
import { Widget, WidgetType, thresholdState, optNum, spanOf, moveItem } from '../lib/widgets';
import { GaugeWidget, BarWidget, StatusWidget, TableWidget, StatWidget, IndicatorWidget, AlarmsWidget, NoteWidget } from '../components/Widgets';

interface Dashboard { id: string; name: string; customer_id?: string | null; layout: { widgets?: Widget[] }; }

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
  const [wall, setWall] = useState(false);
  const [dragFrom, setDragFrom] = useState<number | null>(null);
  const [name, setName] = useState('');
  // create form
  const [newName, setNewName] = useState('');
  const [tpl, setTpl] = useState('blank');
  const [tplDevice, setTplDevice] = useState('');
  // widget form
  const [wText, setWText] = useState('');
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

  async function exportBoard() {
    if (!sel) return;
    const d = await api<unknown>(`/v1/dashboards/${sel.id}/export`).catch(e => { setErr(String(e)); return null; });
    if (!d) return;
    const a = document.createElement('a');
    a.href = URL.createObjectURL(new Blob([JSON.stringify(d, null, 2)], { type: 'application/json' }));
    a.download = `${sel.name.replace(/[^\w-]+/g, '_') || 'dashboard'}.json`;
    a.click();
    URL.revokeObjectURL(a.href);
  }
  async function importFile(e: React.ChangeEvent<HTMLInputElement>) {
    const f = e.target.files?.[0];
    e.target.value = '';
    if (!f) return;
    setErr('');
    try {
      const r = await api<{ id: string; missing_devices: string[] }>('/v1/dashboards/import', { method: 'POST', body: await f.text() });
      await loadBoards();
      if (r.missing_devices.length) setErr(`Imported. These devices do not exist here, so their widgets show no data until repointed: ${r.missing_devices.join(', ')}`);
    } catch (e2) { setErr(String(e2)); }
  }
  const [me, setMe] = useState<{ role: string; customer_id?: string } | null>(null);
  useEffect(() => { api<{ role: string; customer_id?: string }>('/v1/me').then(setMe).catch(() => {}); }, []);
  const scoped = !!me?.customer_id;
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
    if (sel && (wType === 'alarms' || wType === 'note')) {
      const nw: Widget = { id: uid(), type: wType, device_id: '', title: wTitle.trim() || (wType === 'alarms' ? 'Open alerts' : 'Note'), text: wType === 'note' ? wText.slice(0, 2000) : undefined };
      setSel({ ...sel, layout: { widgets: [...(sel.layout.widgets ?? []), nw] } });
      setWTitle(''); setWText('');
      return;
    }
    if (!sel || !wDevice) return;
    const w: Widget = {
      id: uid(), type: wType, device_id: wDevice, point_id: wPoint || undefined,
      title: wTitle.trim() || `${deviceName(wDevice)} ${wType === 'bar' || wType === 'status' || wType === 'table' ? '' : wPoint || ''}${wType === 'timeseries' ? ' (24h)' : ''}`.trim(),
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

  const setWidgets = (ws: Widget[]) => sel && setSel({ ...sel, layout: { widgets: ws } });
  const resize = (id: string, delta: number) => setWidgets(widgets.map(w => w.id === id ? { ...w, span: Math.min(4, Math.max(1, spanOf(w) + delta)) } : w));

  // Wall mode with several dashboards: rotate through those that have widgets.
  useEffect(() => {
    if (!wall) return;
    const rotating = boards.filter(b => (b.layout.widgets ?? []).length > 0);
    if (rotating.length < 2) return;
    const t = setInterval(() => {
      setSel(cur => {
        const i = rotating.findIndex(b => b.id === cur?.id);
        return rotating[(i + 1) % rotating.length];
      });
    }, 30000);
    return () => clearInterval(t);
  }, [wall, boards]);

  // Wall / TV mode: hides navigation, goes fullscreen when allowed, Esc exits.
  useEffect(() => {
    document.body.classList.toggle('wall', wall);
    if (wall) document.documentElement.requestFullscreen?.().catch(() => undefined);
    const onKey = (e: KeyboardEvent) => { if (e.key === 'Escape') setWall(false); };
    const onFs = () => { if (!document.fullscreenElement) setWall(false); };
    window.addEventListener('keydown', onKey);
    document.addEventListener('fullscreenchange', onFs);
    return () => {
      window.removeEventListener('keydown', onKey);
      document.removeEventListener('fullscreenchange', onFs);
      document.body.classList.remove('wall');
      if (wall && document.fullscreenElement) document.exitFullscreen?.().catch(() => undefined);
    };
  }, [wall]);

  return (
    <>
      {!wall && <h1>Dashboards</h1>}
      {err && <p className="muted">{err}</p>}

      {!wall && !scoped && <div className="card" style={{ marginBottom: 16 }}>
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
          <label className="ghost" style={{ cursor: 'pointer', alignSelf: 'flex-end' }}>
            Import file
            <input type="file" accept="application/json,.json" aria-label="Import a dashboard file" style={{ display: 'none' }} onChange={importFile} />
          </label>
        </div>
        <div className="muted" style={{ fontSize: 12, marginTop: 6 }}>{TEMPLATES.find(t => t.id === tpl)?.desc}</div>
      </div>}

      {!wall && <div style={{ display: 'flex', gap: 8, flexWrap: 'wrap', marginBottom: 16 }}>
        {boards.map(b => (
          <button key={b.id} className={sel?.id === b.id ? '' : 'ghost'} onClick={() => { setSel(b); setName(b.name); setEditing(false); }}>{b.name}</button>
        ))}
        {boards.length === 0 && <span className="muted">{scoped ? 'No dashboards have been shared with you yet.' : 'No dashboards yet.'}</span>}
      </div>}

      {sel && (
        <>
          <div style={{ display: 'flex', gap: 8, alignItems: 'center', marginBottom: 12 }}>
            {editing ? <input value={name} onChange={e => setName(e.target.value)} /> : <h2 style={{ margin: 0 }}>{sel.name}</h2>}
            {wall ? null : editing ? (
              <>
                <button onClick={saveBoard}>Save</button>
                <button className="ghost" onClick={() => { setEditing(false); loadBoards(); }}>Cancel</button>
                <button className="ghost" onClick={deleteBoard}>Delete</button>
              </>
            ) : (
              <>
                {!scoped && <button className="ghost" onClick={() => setEditing(true)}>Edit</button>}
                <button className="ghost" onClick={() => setWall(true)} disabled={widgets.length === 0}>Wall mode</button>
                {!scoped && <button className="ghost" onClick={exportBoard}>Export</button>}
                {me?.role === 'admin' && !scoped && <ShareWithCustomer path={`/v1/dashboards/${sel.id}/customer`} value={sel.customer_id} onDone={loadBoards} onError={setErr} />}
              </>
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
                    <option value="table">Table (all points)</option>
                    <option value="stat">Stats (24h min/avg/max)</option>
                    <option value="indicator">Indicator lamp</option>
                    <option value="alarms">Open alerts list</option>
                    <option value="note">Text note</option>
                  </select>
                </div>
                {wType === 'note' && <div><label>Text</label><textarea rows={3} maxLength={2000} value={wText} onChange={e => setWText(e.target.value)} /></div>}
                {wType !== 'alarms' && wType !== 'note' && <div><label>Device</label>
                  <select value={wDevice} onChange={e => setWDevice(e.target.value)}>
                    {devices.map(d => <option key={d.id} value={d.id}>{d.name || d.id}</option>)}
                  </select>
                </div>}
                {wType !== 'alarms' && wType !== 'note' && <div><label>Point</label>
                  <select value={wPoint} onChange={e => setWPoint(e.target.value)}>
                    {devicePoints.map(p => <option key={p.point_id} value={p.point_id}>{p.point_id}</option>)}
                    {devicePoints.length === 0 && <option value="">no live points</option>}
                  </select>
                </div>}
                <div><label>Title (optional)</label><input value={wTitle} onChange={e => setWTitle(e.target.value)} /></div>
                {(wType === 'gauge' || wType === 'bar') && <div><label>Min</label><input type="number" style={{ width: 80 }} value={wMin} onChange={e => setWMin(e.target.value)} /></div>}
                {(wType === 'gauge' || wType === 'bar') && <div><label>Max</label><input type="number" style={{ width: 80 }} value={wMax} onChange={e => setWMax(e.target.value)} /></div>}
                {wType !== 'timeseries' && wType !== 'status' && wType !== 'stat' && wType !== 'alarms' && wType !== 'note' && <div><label>{wType === 'indicator' ? 'On at (value ≥)' : 'Warn at'}</label><input type="number" style={{ width: 80 }} value={wWarn} onChange={e => setWWarn(e.target.value)} /></div>}
                {wType !== 'timeseries' && wType !== 'status' && wType !== 'stat' && wType !== 'indicator' && wType !== 'alarms' && wType !== 'note' && <div><label>Critical at</label><input type="number" style={{ width: 80 }} value={wCrit} onChange={e => setWCrit(e.target.value)} /></div>}
                <button onClick={addWidget} disabled={wType === 'alarms' || wType === 'note' ? false : !wDevice || (!wPoint && wType !== 'bar' && wType !== 'status' && wType !== 'table')}>Add</button>
              </div>
            </div>
          )}

          {wall && <button className="ghost wall-exit" onClick={() => setWall(false)}>Exit wall mode (Esc)</button>}
          {editing && widgets.length > 1 && <p className="muted" style={{ fontSize: 12 }}>Drag widgets to reorder. Use the size buttons to make a widget wider or narrower.</p>}
          <div className={`cards board${wall ? ' wall-grid' : ''}`}>
            {widgets.map((w, i) => (
              <div key={w.id} className={`board-item${dragFrom === i ? ' dragging' : ''}`}
                style={{ position: 'relative', minWidth: 0, gridColumn: `span ${spanOf(w)}` }}
                draggable={editing}
                onDragStart={() => setDragFrom(i)}
                onDragOver={e => { if (editing && dragFrom !== null) e.preventDefault(); }}
                onDrop={e => { e.preventDefault(); if (dragFrom !== null) setWidgets(moveItem(widgets, dragFrom, i)); setDragFrom(null); }}
                onDragEnd={() => setDragFrom(null)}>
                {w.type === 'kpi' ? <KpiWidget w={w} /> : w.type === 'gauge' ? <GaugeWidget w={w} /> : w.type === 'bar' ? <BarWidget w={w} /> : w.type === 'status' ? <StatusWidget w={w} /> : w.type === 'table' ? <TableWidget w={w} /> : w.type === 'stat' ? <StatWidget w={w} /> : w.type === 'indicator' ? <IndicatorWidget w={w} /> : w.type === 'alarms' ? <AlarmsWidget w={w} /> : w.type === 'note' ? <NoteWidget w={w} /> : <SeriesWidget w={w} />}
                {editing && (
                  <div className="board-tools">
                    <button className="ghost" aria-label="Narrower" onClick={() => resize(w.id, -1)}>-</button>
                    <button className="ghost" aria-label="Wider" onClick={() => resize(w.id, 1)}>+</button>
                    <button className="ghost" aria-label="Move earlier" onClick={() => setWidgets(moveItem(widgets, i, i - 1))}>&lt;</button>
                    <button className="ghost" aria-label="Move later" onClick={() => setWidgets(moveItem(widgets, i, i + 1))}>&gt;</button>
                    <button className="ghost" aria-label="Remove widget" onClick={() => removeWidget(w.id)}>x</button>
                  </div>
                )}
              </div>
            ))}
            {widgets.length === 0 && <p className="muted">No widgets. {editing ? 'Add one above.' : 'Click Edit to add widgets.'}</p>}
          </div>
        </>
      )}
    </>
  );
}
