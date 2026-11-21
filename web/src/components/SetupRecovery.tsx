import { useEffect, useRef, useState } from 'react';
import { api } from '../lib/api';
interface SavedSetup { session_id: string; gateway_id: string; serial: string; gateway_status: string; device_id: string | null; }
// Metadata only. Loading or opening recovery never creates/redeems a claim.
export default function SetupRecovery() {
 const pending=useRef(new Set<string>());
 const [opening,setOpening]=useState<Set<string>>(new Set());
 const [uncertain,setUncertain]=useState<Set<string>>(new Set());
 const [saved,setSaved]=useState<SavedSetup[]|null>(null);
 const [error,setError]=useState(''); const [reload,setReload]=useState(0);
 useEffect(()=>{let active=true;setError('');setSaved(null);
  api<SavedSetup[]>('/v1/commissioning/sessions').then(s=>{if(active)setSaved(s);}).catch(()=>{if(active)setError('Saved setup could not be loaded. Do not create another gateway to recover an existing one.');});
  return()=>{active=false;};
 },[reload]);
 async function addSource(gateway:string){if(pending.current.has(gateway))return;pending.current.add(gateway);setOpening(current=>new Set(current).add(gateway));
  try{const result=await api<{session_id:string}>(`/v1/gateways/${encodeURIComponent(gateway)}/source-setup`,{method:'POST',headers:{'Idempotency-Key':crypto.randomUUID()},body:'{}'});window.location.assign(`/onboarding?session=${encodeURIComponent(result.session_id)}`)}
  catch{setUncertain(current=>new Set(current).add(gateway));}
  finally{setOpening(current=>{const next=new Set(current);next.delete(gateway);return next;});}
 }
 return <section aria-label="Saved gateway setup" className="card" style={{marginBottom:24}}>
  <h2>Continue a saved setup</h2>
  <p className="muted">Pick your gateway to continue. You do not need another claim code. Paired means setup was saved, not that readings are arriving.</p>
  {uncertain.size>0&&<p role="alert">Could not confirm whether source setup was started. Reload saved setups and look for a setup with no source assigned. This only reads saved setups and does not resend the request. If it is missing, keep this page open and ask for help before starting another setup. No new gateway or claim code was requested.</p>}
  {!saved&&!error&&<p role="status">Loading saved setups...</p>}
  {error&&<p role="alert">{error}</p>}
  {(saved!==null||error)&&<button type="button" className="secondary" onClick={()=>setReload(x=>x+1)}>{error?'Try loading again':'Reload saved setups'}</button>}
  {saved?.length===0&&<p>No saved setup found in the latest 50 sessions.</p>}
  {saved&&saved.length>0&&<ul style={{listStyle:'none',padding:0}}>{saved.map(s=><li key={s.session_id} style={{padding:'12px 0',borderBottom:'1px solid var(--border, #ddd)'}}>
   <strong>Gateway {s.serial}</strong><p className="muted">{s.gateway_status==='active'?'Paired identity saved':'Waiting for pairing'}{s.device_id?' · Source configured':' · No source assigned'}</p>
   <a href={`/onboarding?session=${encodeURIComponent(s.session_id)}`}>Continue setup for {s.serial}</a>
   {s.device_id&&s.gateway_status==='active'&&<button type="button" className="secondary" style={{marginLeft:12}} disabled={pending.current.has(s.gateway_id)} onClick={()=>void addSource(s.gateway_id)}>{opening.has(s.gateway_id)?'Opening source setup...':uncertain.has(s.gateway_id)?'Source setup outcome unknown':'Add another source'}</button>}
  </li>)}</ul>}
  {saved&&<p className="muted">Up to 50 recent setups. A missing setup in this list does not prove that an earlier request failed.</p>}
 </section>;
}
