import { useEffect, useState } from 'react';
import { api } from '../lib/api';

interface PointDef { id: string; register?: number; key?: string; node_id?: string; func?: number; type?: string; word_order?: string; scale?: number; unit?: string; min?: number; max?: number; }
interface ProfileRow { id: string; name: string; driver_profile: string; points: PointDef[]; created_at: string; }

const emptyPoint: PointDef = { id: '', register: 0, func: 4, type: 'u16', word_order: 'abcd', scale: 1, unit: '', min: 0, max: 1000000 };

export default function Profiles() {
  const [profiles, setProfiles] = useState<ProfileRow[]>([]);
  const [name, setName] = useState('');
  const [driver, setDriver] = useState('modbus-generic');
  const [points, setPoints] = useState<PointDef[]>([{ ...emptyPoint }]);
  const [msg, setMsg] = useState('');
  const isModbus = driver.startsWith('modbus');
  const cols = isModbus ? 'minmax(0,1.2fr) minmax(0,.7fr) minmax(0,.6fr) minmax(0,.7fr) minmax(0,.8fr) minmax(0,.6fr) minmax(0,.6fr) minmax(0,.8fr) minmax(0,.8fr) 30px' : driver === 'opcua' ? 'minmax(0,1fr) minmax(0,2fr) minmax(0,.6fr) minmax(0,.6fr) minmax(0,.8fr) minmax(0,.8fr) 30px' : 'minmax(0,1fr) minmax(0,1.4fr) minmax(0,.6fr) minmax(0,.6fr) minmax(0,.8fr) minmax(0,.8fr) 30px';

  const load = () => api<ProfileRow[]>('/v1/profiles').then(setProfiles).catch(e => setMsg(String(e)));
  useEffect(() => { load(); }, []);

  function setPoint(i: number, k: keyof PointDef, v: string) {
    setPoints(ps => ps.map((p, j) => (j === i ? { ...p, [k]: ['register', 'func', 'scale', 'min', 'max'].includes(k) ? +v : v } : p)));
  }

  // Send only the fields the chosen driver understands.
  function clean(p: PointDef): PointDef {
    const base = { id: p.id, scale: p.scale, unit: p.unit, min: p.min, max: p.max };
    if (driver === 'opcua') return { ...base, node_id: p.node_id };
    if (driver === 'serial-json') return { ...base, ...(p.key ? { key: p.key } : {}) };
    return { ...base, register: p.register, func: p.func, type: p.type, word_order: p.word_order };
  }

  async function submit(e: React.FormEvent) {
    e.preventDefault();
    setMsg('');
    try {
      await api('/v1/profiles', { method: 'POST', body: JSON.stringify({ name, driver_profile: driver, points: points.filter(p => p.id).map(clean) }) });
      setName(''); setPoints([{ ...emptyPoint }]);
      load();
    } catch (e2) { setMsg(String(e2)); }
  }

  return (
    <>
      <h1>Sensor profiles</h1>
      <p className="muted">A profile teaches the edge agent how to read a sensor model: which registers, which data type, which scale. Add a profile here, then onboard devices with it - no code changes.</p>
      <div className="card" style={{ maxWidth: 900, marginBottom: 20 }}>
        <b>New profile</b>
        <form onSubmit={submit} style={{ maxWidth: 'none' }}>
          <label>Name</label>
          <input value={name} onChange={e => setName(e.target.value)} placeholder="Acme EM-300 power meter" required />
          <label>Driver</label>
          <select value={driver} onChange={e => setDriver(e.target.value)}>
            <option value="modbus-generic">Modbus RTU (generic)</option>
            <option value="modbus-energy-meter">Modbus energy meter (legacy)</option>
            <option value="modbus-tcp">Modbus TCP (PLC, meter, SCADA RTU)</option>
            <option value="opcua">OPC UA (SCADA, Siemens, Beckhoff, Kepware, Ignition)</option>
            <option value="serial-json">STM32 / Arduino / ESP32 over UART or USB (JSON, key=value, CSV)</option>
            <option value="door-contact">Door contact (legacy)</option>
          </select>
          <label>Points</label>
          <div className="muted" style={{ display: 'grid', gridTemplateColumns: cols, gap: 4, fontSize: 11, marginBottom: 2 }}>
            <span>id</span>
            {driver === 'opcua' && <span>node id</span>}
            {driver === 'serial-json' && <span>field</span>}
            {isModbus && <><span>register</span><span>function</span><span>type</span><span>word order</span></>}
            <span>scale</span><span>unit</span><span>min</span><span>max</span><span />
          </div>
          {points.map((p, i) => (
            <div key={i} style={{ display: 'grid', gridTemplateColumns: cols, gap: 4, marginBottom: 6 }}>
              <input value={p.id} onChange={e => setPoint(i, 'id', e.target.value)} placeholder="temp" required />
              {driver === 'opcua' && <input value={p.node_id ?? ''} onChange={e => setPoint(i, 'node_id', e.target.value)} placeholder="ns=2;s=Boiler.Temp" required />}
              {driver === 'serial-json' && <input value={p.key ?? ''} onChange={e => setPoint(i, 'key', e.target.value)} placeholder="field name (default: id)" />}
              {isModbus && <input type="number" value={p.register ?? 0} onChange={e => setPoint(i, 'register', e.target.value)} title="register" />}
              {isModbus && (
                <select value={p.func} onChange={e => setPoint(i, 'func', e.target.value)}>
                  <option value={1}>coil</option><option value={2}>discrete</option><option value={3}>holding</option><option value={4}>input</option>
                </select>
              )}
              {isModbus && (
                <select value={p.type} onChange={e => setPoint(i, 'type', e.target.value)}>
                  {['u16', 'i16', 'u32', 'i32', 'f32', 'bool'].map(t => <option key={t}>{t}</option>)}
                </select>
              )}
              {isModbus && (
                <select value={p.word_order} onChange={e => setPoint(i, 'word_order', e.target.value)}>
                  {['abcd', 'badc', 'cdab', 'dcba'].map(w => <option key={w}>{w}</option>)}
                </select>
              )}
              <input type="number" step="any" value={p.scale} onChange={e => setPoint(i, 'scale', e.target.value)} title="scale" />
              <input value={p.unit ?? ''} onChange={e => setPoint(i, 'unit', e.target.value)} placeholder="unit" />
              <input type="number" step="any" value={p.min} onChange={e => setPoint(i, 'min', e.target.value)} title="min" />
              <input type="number" step="any" value={p.max} onChange={e => setPoint(i, 'max', e.target.value)} title="max" />
              <button type="button" className="ghost" onClick={() => setPoints(ps => ps.filter((_, j) => j !== i))} disabled={points.length === 1}>x</button>
            </div>
          ))}
          <button type="button" className="ghost" onClick={() => setPoints(ps => [...ps, { ...emptyPoint }])}>+ point</button>
          <div style={{ marginTop: 14 }}><button type="submit">Create profile</button></div>
        </form>
        {msg && <p className="muted">{msg}</p>}
      </div>
      {profiles.map(p => (
        <div key={p.id} className="card" style={{ marginBottom: 10 }}>
          <b>{p.name}</b> <span className="muted">{p.driver_profile} - {p.points.length} points</span>
          <div className="muted">{p.points.map(pt => pt.node_id ? `${pt.id}@${pt.node_id}` : pt.key ? `${pt.id}:${pt.key}` : `${pt.id}@r${pt.register}:${pt.type ?? 'u16'}`).join(', ')}</div>
        </div>
      ))}
      {profiles.length === 0 && <p className="muted">No profiles yet.</p>}
    </>
  );
}
