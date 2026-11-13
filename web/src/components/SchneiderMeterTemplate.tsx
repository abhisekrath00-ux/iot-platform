import {useState} from 'react';
import {api} from '../lib/api';
import {EDGE_TEMPLATES} from '../lib/edgeSetup';
import {templatePoints} from '../lib/registerMap';
export interface SavedMeterProfile {id:string;name:string;driver_profile:string;connection_defaults:Record<string,unknown>;points:ReturnType<typeof templatePoints>}
// A supplied map, not a guessed connection. Saving never sends a gateway command.
export default function SchneiderMeterTemplate({onSaved}:{onSaved:(p:SavedMeterProfile)=>void}){
 const t=EDGE_TEMPLATES.find(t=>t.id==='schneider-em1x00-em1220')!;
 const [model,setModel]=useState(''),[order,setOrder]=useState(''),[base,setBase]=useState(''),[review,setReview]=useState(false),[busy,setBusy]=useState(false),[error,setError]=useState('');
 async function save(){if(busy||model!=='EM1220'||!order||!base||!review)return;setBusy(true);setError('');try{
 const body={name:t.name,driver_profile:'modbus-generic',connection_defaults:t.setupDefaults!,points:templatePoints(t.id).map(p=>({...p,word_order:order,register:base==='minus1'?p.register-1:p.register}))};
 const created=await api<{id:string}>('/v1/profiles',{method:'POST',body:JSON.stringify(body)});onSaved({...body,id:created.id});
 }catch(e){setError(String(e));}finally{setBusy(false);}}
 return <details className="card" style={{marginBottom:16}}><summary>Use the saved Schneider EM1X00 parameter template</summary>
 <p>The supplied parameter names and addresses are ready. No register re-entry is needed. Confirm the remaining meter settings once, then reuse this profile.</p>
 <p>Connection preset: 9600 baud, 8 data bits, even parity, 1 stop bit. This is your supplied UART setting. USB COM port and slave ID still need confirming. Selecting or saving this template does not scan ports or start collection.</p>
 <label>Exact model on the meter<select aria-label="Schneider exact model" value={model} onChange={e=>setModel(e.target.value)}><option value="">Choose...</option><option>EM1220</option><option>EM1200</option><option>EM1000</option><option value="unknown">Not sure</option></select></label>
 {model&&model!=='EM1220'&&<p role="alert">This full supplied map includes current, voltage and frequency, which the matching manual lists for EM1220 only. Check the nameplate before using it. Other models need a model-specific map.</p>}
 <label>Verified word order from the working device test<select aria-label="Schneider word order" value={order} onChange={e=>setOrder(e.target.value)}><option value="">Choose...</option>{['abcd','badc','cdab','dcba'].map(x=><option key={x}>{x}</option>)}</select></label>
 <p className="muted">The manual says ModScan32 "Swapped FP". That name alone does not prove which byte/word order this driver needs. Use the tested setting, not a guess.</p>
 <label>Verified address convention<select aria-label="Schneider register base" value={base} onChange={e=>setBase(e.target.value)}><option value="">Choose...</option><option value="listed">Listed number is wire address</option><option value="minus1">Listed number minus 1 is wire address</option></select></label>
 <details><summary>Review the supplied parameters ({t.points.length})</summary><div style={{overflowX:'auto'}}><table><thead><tr><th>Parameter</th><th>Description</th><th>Listed address</th></tr></thead><tbody>{t.points.map(p=><tr key={p.id}><td>{p.name??p.id}</td><td>{p.description}</td><td>{p.register}</td></tr>)}</tbody></table></div><p>FC03, Float32, scale 1. Listed addresses are not measured values. Unsupported or unselected parameters may return zero. No energy counters were included in the supplied table.</p></details>
 <label style={{display:'flex',gap:8,alignItems:'start'}}><input style={{width:'auto'}} type="checkbox" checked={review} onChange={e=>setReview(e.target.checked)}/>I checked the exact model and settings against the working meter. These are read-only measurement registers.</label>
 <button type="button" disabled={busy||model!=='EM1220'||!order||!base||!review} onClick={save}>{busy?'Saving profile...':'Save and select this meter profile'}</button>{error&&<p role="alert">{error}</p>}
 </details>;
}
