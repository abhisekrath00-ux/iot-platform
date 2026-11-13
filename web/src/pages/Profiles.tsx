import { useEffect, useState } from 'react';
import { api, EXTRA_DRIVERS } from '../lib/api';
import { EDGE_TEMPLATES } from '../lib/edgeSetup';
import { Issue, MapPoint, parseAny, templatePoints, toCsv, validateMap } from '../lib/registerMap';

interface PointDef { name?: string; description?:string; id: string; register?: number; key?: string; node_id?: string; oid?: string; ioa?: number; func?: number; type?: string; word_order?: string; scale?: number; unit?: string; min?: number; max?: number; }
interface ProfileRow { id: string; name: string; driver_profile: string; points: PointDef[]; created_at: string; }

const emptyPoint: PointDef = { id: '', register: 0, func: 4, type: 'u16', word_order: 'abcd', scale: 1, unit: '', min: 0, max: 1000000 };

export default function Profiles() {
  const [profiles, setProfiles] = useState<ProfileRow[]>([]);
  const [name, setName] = useState('');
  const [driver, setDriver] = useState('modbus-generic');
  const [points, setPoints] = useState<PointDef[]>([{ ...emptyPoint }]);
  const [msg, setMsg] = useState('');
  const [connectionDefaults,setConnectionDefaults]=useState<Record<string,unknown>>({});
  const isModbus = driver.startsWith('modbus');
  const [paste, setPaste] = useState('');
  const [issues, setIssues] = useState<Issue[]>([]);
  const [pendingTpl, setPendingTpl] = useState<string | null>(null);
  const [pendingOk, setPendingOk] = useState(false);
  const [pendingOrder,setPendingOrder]=useState(''),[pendingBase,setPendingBase]=useState(''),[pendingFunc,setPendingFunc]=useState('');
  const liveIssues = isModbus && points.some(p => p.id) ? validateMap(points.filter(p => p.id) as MapPoint[]) : [];
  const shown = issues.length ? issues : liveIssues;
  function loadMap(pts: MapPoint[], iss: Issue[]) { if (pts.length) setPoints(pts); setIssues(iss); }
  function save(name: string, text: string) { const u = URL.createObjectURL(new Blob([text], { type: 'text/plain' })); const a = document.createElement('a'); a.href = u; a.download = name; a.click(); URL.revokeObjectURL(u); }
  const extra = EXTRA_DRIVERS[driver];


  const load = () => api<ProfileRow[]>('/v1/profiles').then(setProfiles).catch(e => setMsg(String(e)));
  useEffect(() => { load(); }, []);

  function setPoint(i: number, k: keyof PointDef, v: string) {
    setPoints(ps => ps.map((p, j) => (j === i ? { ...p, [k]: ['register', 'func', 'scale', 'min', 'max', 'ioa'].includes(k) ? +v : v } : p)));
  }

  // Send only the fields the chosen driver understands.
  function clean(p: PointDef): PointDef {
    const base = { id: p.id, name:p.name, description:p.description, scale: p.scale, unit: p.unit, min: p.min, max: p.max };
    if (driver === 'opcua') return { ...base, node_id: p.node_id };
    if (extra) return { ...base, ...(extra.f === 'oid' ? { oid: p.oid } : extra.f === 'ioa' ? { ioa: p.ioa } : { key: p.key }), ...(extra.reg ? { register: p.register } : {}) };
    if (driver === 'serial-json') return { ...base, ...(p.key ? { key: p.key } : {}) };
    return { ...base, register: p.register, func: p.func, type: p.type, word_order: p.word_order };
  }

  async function submit(e: React.FormEvent) {
    e.preventDefault();
    setMsg('');
    try {
      await api('/v1/profiles', { method: 'POST', body: JSON.stringify({ name, driver_profile: driver, connection_defaults:connectionDefaults, points: points.filter(p => p.id).map(clean) }) });
      setName('');setConnectionDefaults({}); setPoints([{ ...emptyPoint }]);
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
          <select aria-label="Profile driver" value={driver} onChange={e => {setDriver(e.target.value);setConnectionDefaults({});}}>
            <option value="modbus-generic">Modbus RTU (generic)</option>
            <option value="modbus-energy-meter">Modbus energy meter (legacy)</option>
            <option value="modbus-tcp">Modbus TCP (PLC, meter, SCADA RTU)</option>
            <option value="opcua">OPC UA (SCADA, Siemens, Beckhoff, Kepware, Ignition)</option>
            <option value="serial-json">STM32 / Arduino / ESP32 over UART or USB (JSON, key=value, CSV)</option>
            {Object.entries(EXTRA_DRIVERS).map(([k, v]) => <option key={k} value={k}>{v.label}</option>)}
            <option value="door-contact">Door contact (legacy)</option>
          </select>
          {isModbus && (
            <div className="card" style={{ margin: '8px 0' }}>
              <b>Start from a template or import a register map</b>
              <p className="muted">Loads the points into the form below so you can review and edit them. Nothing is saved until you press Create profile. Format: CSV with a header (id, register, func, type, word_order, scale, unit, min, max) or JSON.</p>
              <div style={{ display: 'flex', gap: 8, flexWrap: 'wrap', alignItems: 'center' }}>
                <select style={{ width: '100%', minWidth:0 }} aria-label="Template" defaultValue="" onChange={e => {
                  const t = EDGE_TEMPLATES.find(x => x.id === e.target.value);
                  if (t?.provisional) { setPendingTpl(t.id); setPendingOk(false);setPendingOrder('');setPendingBase('');setPendingFunc(''); }
                  else if (t) { loadMap(templatePoints(t.id), []); if (!name) setName(t.name); }
                  e.target.value = '';
                }}>
                  <option value="">Load a built-in template…</option>
                  {EDGE_TEMPLATES.map(t => <option key={t.id} value={t.id}>{t.name}{t.provisional ? ' (provisional)' : ''}</option>)}
                </select>
                <input type="file" accept=".csv,.json,.txt" aria-label="Import file" style={{ width: '100%', maxWidth:340 }} onChange={async e => { const f = e.target.files?.[0]; if (f) { const r = parseAny(await f.text()); loadMap(r.points, r.issues); } e.target.value = ''; }} />
                <button type="button" className="ghost" onClick={() => save('register-map.csv', toCsv(points.filter(p => p.id) as MapPoint[]))}>Export CSV</button>
                <button type="button" className="ghost" onClick={() => save('register-map.json', JSON.stringify(points.filter(p => p.id), null, 2))}>Export JSON</button>
              </div>
              {pendingTpl && (() => {
                const t = EDGE_TEMPLATES.find(x => x.id === pendingTpl)!;
                return (
                  <div className="muted" style={{ border: '1px solid var(--warn, #b58900)', padding: 8, borderRadius: 6, marginTop: 8 }}>
                    <p style={{ margin: 0 }}><b>Provisional map: {t.name}.</b> {t.provisional}</p>
                    <label style={{ display: 'flex', gap: 6, alignItems: 'center', marginTop: 8, width: '100%' }}>
                      <input type="checkbox" style={{ width: 'auto' }} checked={pendingOk} onChange={e => setPendingOk(e.target.checked)} />
                      I have checked this against the meter or its manual
                    </label>
                    {t.wordOrderUnverified&&<label>Verified word order<select aria-label="Template word order" value={pendingOrder} onChange={e=>setPendingOrder(e.target.value)}><option value="">Choose from verified device settings...</option>{['abcd','badc','cdab','dcba'].map(x=><option key={x}>{x}</option>)}</select></label>}
                    {t.pduBaseUnverified&&<label>Verified register address convention<select aria-label="Template register base" value={pendingBase} onChange={e=>setPendingBase(e.target.value)}><option value="">Choose from verified device settings...</option><option value="listed">Listed number is wire address</option><option value="minus1">Listed number minus 1 is wire address</option></select></label>}
                    {t.funcUnverified&&<label>Verified read function<select aria-label="Template read function" value={pendingFunc} onChange={e=>setPendingFunc(e.target.value)}><option value="">Choose...</option><option value="3">FC03 holding</option><option value="4">FC04 input</option></select></label>}
                    {t.setupDefaults&&<p>Saved connection preset for this supplied map: 9600 baud, 8 data bits, even parity, 1 stop bit. COM port and slave ID still need confirming. No automatic probe is sent.</p>}
                    <div style={{ display: 'flex', gap: 8, marginTop: 8 }}>
                      <button type="button" className="ghost" disabled={!pendingOk||(t.wordOrderUnverified&&!pendingOrder)||(t.pduBaseUnverified&&!pendingBase)||(t.funcUnverified&&!pendingFunc)} onClick={() => { loadMap(templatePoints(t.id).map(p=>({...p,register:pendingBase==='minus1'?p.register-1:p.register,word_order:pendingOrder||p.word_order,func:pendingFunc?+pendingFunc:p.func})), []); if (!name) setName(t.name);setConnectionDefaults(t.setupDefaults??{}); setPendingTpl(null); }}>Load {t.name}</button>
                      <button type="button" className="ghost" onClick={() => setPendingTpl(null)}>Cancel</button>
                    </div>
                  </div>
                );
              })()}
              <textarea aria-label="Paste a register map" rows={3} style={{ width: '100%', marginTop: 8 }} placeholder="Or paste CSV / JSON here" value={paste} onChange={e => setPaste(e.target.value)} />
              <button type="button" className="ghost" disabled={!paste.trim()} onClick={() => { const r = parseAny(paste); loadMap(r.points, r.issues); }}>Import pasted text</button>
              {shown.length > 0 && <ul role="alert" style={{ color: '#dc2626', margin: '8px 0 0', paddingLeft: 18 }}>{shown.slice(0, 12).map((i, k) => <li key={k}>{i.row ? `Row ${i.row}: ` : ''}{i.text}</li>)}{shown.length > 12 && <li>…and {shown.length - 12} more</li>}</ul>}
              {!shown.length && points.some(p => p.id) && <p className="muted" style={{ color: '#16a34a' }}>Register map checks passed (ids unique, no overlaps, types and ranges valid).</p>}
            </div>
          )}
          <h3>Points ({points.length})</h3>
          <p className="muted">Review each point's address, decoding and accepted range. These checks validate the map structure, not the device model or its reported units.</p>
          {points.map((p,i)=>{
            const field=(label:string,control:React.ReactNode)=><label style={{display:'flex',flexDirection:'column',gap:5,margin:0,minWidth:0}}>{label}{control}</label>;
            return <fieldset key={i} style={{minWidth:0,border:'1px solid var(--line)',borderRadius:14,padding:16,margin:'14px 0'}}>
             <legend style={{padding:'0 6px'}}>Point {i+1}{p.name?` - ${p.name}`:p.id?` - ${p.id}`:''}</legend>{p.description&&<p className="muted">{p.description}</p>}
             <div style={{display:'grid',gridTemplateColumns:'repeat(auto-fit,minmax(min(100%,180px),1fr))',gap:12}}>
              {field('Point ID',<input aria-label={`Point ${i+1} ID`} value={p.id} onChange={e=>setPoint(i,'id',e.target.value)} placeholder="temp" required/>)}
              {driver==='opcua'&&field('OPC UA node ID',<input aria-label={`Point ${i+1} node ID`} value={p.node_id??''} onChange={e=>setPoint(i,'node_id',e.target.value)} placeholder="ns=2;s=Boiler.Temp" required/>)}
              {extra&&field(extra.f==='oid'?'Numeric OID':extra.f==='ioa'?'IOA':'Protocol key',<input aria-label={`Point ${i+1} protocol address`} value={extra.f==='oid'?(p.oid??''):extra.f==='ioa'?(p.ioa??''):(p.key??'')} onChange={e=>setPoint(i,extra.f,e.target.value)} placeholder={extra.ph} required/>)}
              {extra?.reg&&field('Point index',<input aria-label={`Point ${i+1} index`} type="number" value={p.register??0} onChange={e=>setPoint(i,'register',e.target.value)}/>)}
              {driver==='serial-json'&&field('Field name (default: ID)',<input aria-label={`Point ${i+1} field name`} value={p.key??''} onChange={e=>setPoint(i,'key',e.target.value)}/>)}
              {isModbus&&<>
               {field('Register (zero-based)',<input aria-label={`Point ${i+1} register`} type="number" value={p.register??0} onChange={e=>setPoint(i,'register',e.target.value)}/>)}
               {field('Read function',<select aria-label={`Point ${i+1} function`} value={p.func} onChange={e=>setPoint(i,'func',e.target.value)}><option value={1}>1 - coils</option><option value={2}>2 - discrete inputs</option><option value={3}>3 - holding registers</option><option value={4}>4 - input registers</option></select>)}
               {field('Value type',<select aria-label={`Point ${i+1} type`} value={p.type} onChange={e=>setPoint(i,'type',e.target.value)}>{['u16','i16','u32','i32','f32','bool'].map(t=><option key={t}>{t}</option>)}</select>)}
               {field('Word / byte order',<select aria-label={`Point ${i+1} word order`} value={p.word_order} onChange={e=>setPoint(i,'word_order',e.target.value)}>{['abcd','badc','cdab','dcba'].map(w=><option key={w}>{w}</option>)}</select>)}
              </>}
              {field('Scale multiplier',<input aria-label={`Point ${i+1} scale`} type="number" step="any" value={p.scale} onChange={e=>setPoint(i,'scale',e.target.value)}/>)}
              {field('Unit label',<input aria-label={`Point ${i+1} unit`} value={p.unit??''} onChange={e=>setPoint(i,'unit',e.target.value)} placeholder="unit"/>)}
              {field('Minimum accepted value',<input aria-label={`Point ${i+1} minimum`} type="number" step="any" value={p.min} onChange={e=>setPoint(i,'min',e.target.value)}/>)}
              {field('Maximum accepted value',<input aria-label={`Point ${i+1} maximum`} type="number" step="any" value={p.max} onChange={e=>setPoint(i,'max',e.target.value)}/>)}
             </div>
             <button style={{marginTop:12}} type="button" className="ghost" aria-label={`Remove point ${i+1}`} disabled={points.length===1} onClick={()=>setPoints(ps=>ps.filter((_,j)=>j!==i))}>Remove point</button>
            </fieldset>
          })}
          <button type="button" className="ghost" onClick={() => setPoints(ps => [...ps, { ...emptyPoint }])}>+ point</button>
          <div style={{ marginTop: 14 }}><button type="submit" disabled={liveIssues.length > 0} title={liveIssues.length ? 'Fix the register map problems first' : undefined}>Create profile</button></div>
        </form>
        {msg && <p className="muted">{msg}</p>}
      </div>
      {profiles.map(p => (
        <div key={p.id} className="card" style={{ marginBottom: 10 }}>
          <b>{p.name}</b> <span className="muted">{p.driver_profile} - {p.points.length} points</span>
          <div className="muted">{p.points.map(pt => pt.oid ? `${pt.id}@${pt.oid}` : pt.ioa ? `${pt.id}@ioa${pt.ioa}` : pt.node_id ? `${pt.id}@${pt.node_id}` : pt.key ? `${pt.id}:${pt.key}` : `${pt.id}@r${pt.register}:${pt.type ?? 'u16'}`).join(', ')}</div>
        </div>
      ))}
      {profiles.length === 0 && <p className="muted">No profiles yet.</p>}
    </>
  );
}
