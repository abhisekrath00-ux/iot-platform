import { useEffect, useRef, useState } from 'react';
import { api } from '../lib/api';
interface SavedSetup { session_id: string; gateway_id: string; serial: string; gateway_status: string; device_id: string | null; }
// Metadata only. Loading or opening recovery never creates/redeems a claim.
export default function SetupRecovery() {
 const pending=useRef(new Set<string>());const [creating,setCreating]=useState('');const [createError,setCreateError]=useState('');
 const [saved,setSaved]=useState<SavedSetup[]|null>(null);
 const [error,setError]=useState(''); const [reload,setReload]=useState(0);
 useEffect(()=>{let active=true;setError('');setSaved(null);
  api<SavedSetup[]>('/v1/commissioning/sessions').then(s=>{if(active)setSaved(s);}).catch(()=>{if(active)setError('Saved setup could not be loaded. Do not create another gateway to recover an existing one.');});
  return()=>{active=false;};
 },[reload]);
 async function addSource(gateway:string){if(pending.current.has(gateway))return;pending.current.add(gateway);setCreating(gateway);setCreateError('');
  try{const result=await api<{session_id:string}>(`/v1/gateways/${encodeURIComponent(gateway)}/source-setup`,{method:'POST',headers:{'Idempotency-Key':crypto.randomUUID()},body:'{}'});window.location.assign(`/onboarding?session=${encodeURIComponent(result.session_id)}`)}
  catch{setCreateError('Could not confirm whether source setup was started. Reload saved setups to recover it before trying again. No new gateway or claim code was requested.');}
  finally{setCreating('')}
 }
 return <section aria-label="Saved gateway setup" className="card" style={{marginBottom:24}}>
  <h2>Continue a saved setup</h2>
  <p className="muted">Pick your gateway to continue. You do not need another claim code. Paired means setup was saved, not that readings are arriving.</p>
  {!saved&&!error&&<p role="status">Loading saved setups...</p>}
  {error&&<><p role="alert">{error}</p><button type="button" onClick={()=>setReload(x=>x+1)}>Try loading again</button></>}
  {saved?.length===0&&<p>No saved setup found in the latest 50 sessions.</p>}
  {createError&&<p role="alert">{createError}</p>}
  {saved&&saved.length>0&&<ul style={{listStyle:'none',padding:0}}>{saved.map(s=><li key={s.session_id} style={{padding:'12px 0',borderBottom:'1px solid var(--border, #ddd)'}}>
   <strong>Gateway {s.serial}</strong><p className="muted">{s.gateway_status==='active'?'Paired identity saved':'Waiting for pairing'}{s.device_id?' · Source configured':' · No source assigned'}</p>
   <a href={`/onboarding?session=${encodeURIComponent(s.session_id)}`}>Continue setup for {s.serial}</a>
   {s.device_id&&s.gateway_status==='active'&&<button type="button" className="secondary" style={{marginLeft:12}} disabled={pending.current.has(s.gateway_id)} onClick={()=>void addSource(s.gateway_id)}>{creating===s.gateway_id?'Opening source setup...':'Add another source'}</button>}
  </li>)}</ul>}
  {saved&&<p className="muted">Up to 50 recent setups.</p>}
 </section>;
}
