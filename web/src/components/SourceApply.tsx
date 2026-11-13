import {useEffect,useRef,useState} from 'react';
import {api} from '../lib/api';
interface Push {id:string;status:string;detail?:string;version:number;sha256?:string;}
interface Ready {server_ready:boolean;reason:string;desired_sha256:string;source_names:string[];}
interface Me {role:string;via_key?:boolean;customer_id?:string;}
const messages:Record<string,string>={sent:'Sent to gateway. Waiting for its answer.',applied:'Gateway saved the settings. Waiting for its connection check after reload.',confirmed:'Gateway confirmed the settings after reload. Check a live source reading next.',rejected:'Gateway did not confirm applying these settings. Review its response before retrying.',rolled_back:'Gateway restored its previous settings after the connection check failed.',failed:'Settings could not be sent.',expired:'No gateway answer arrived before this request expired.'};
export default function SourceApply({gateway}:{gateway:string}){
 const [ready,setReady]=useState<Ready|null>(null);
 const [me,setMe]=useState<Me|null>(null),[pushes,setPushes]=useState<Push[]>([]),[error,setError]=useState(''),[busy,setBusy]=useState(false),[review,setReview]=useState(false),[uncertain,setUncertain]=useState(false);
 const attempt=useRef(false),generation=useRef(0);
 useEffect(()=>{let active=true;api<Me>('/v1/me').then(x=>{if(active)setMe(x)}).catch(()=>{});return()=>{active=false}},[]);
 const allowed=me?.role==='admin'&&!me.via_key&&!me.customer_id;
 useEffect(()=>{if(!allowed)return;let active=true;
  const load=()=>{const seq=++generation.current;return Promise.all([api<Push[]>(`/v1/gateways/${encodeURIComponent(gateway)}/config-push`),api<Ready>(`/v1/gateways/${encodeURIComponent(gateway)}/source-readiness`)]).then(([x,r])=>{if(active&&seq===generation.current){setPushes(x);setReady(r);setError('')}}).catch(()=>{if(active)setError('Could not check gateway application status. Do not send another request until status can be checked.');});};
  load();const t=setInterval(load,5000);return()=>{active=false;clearInterval(t)};
 },[gateway,allowed]);
 async function send(){if(attempt.current||!review||!ready?.server_ready||error||pushes.some(p=>['sent','applied'].includes(p.status)))return;attempt.current=true;++generation.current;setBusy(true);setError('');
  try{const r=await api<{push_id:string;version:number;status:string;sha256?:string}>(`/v1/gateways/${encodeURIComponent(gateway)}/config-push`,{method:'POST',body:JSON.stringify({expected_sha256:ready.desired_sha256})});setPushes([{id:r.push_id,version:r.version,status:r.status,sha256:r.sha256}]);}
  catch{setUncertain(true);setError('Could not confirm whether this request was sent. Check its saved status before trying again. No automatic retry was sent.');}
  finally{setBusy(false)}
 }
 if(!allowed)return <p className="muted">A workspace admin can apply saved sources to the gateway. Saving a source does not apply it.</p>;
 const current=pushes[0],matches=!!current&&current.sha256===ready?.desired_sha256,open=current&&['sent','applied'].includes(current.status);
 return <section className="card" aria-label="Apply source settings" style={{margin:'16px 0'}}><h3>Apply saved sources to this gateway</h3>
 <p>The gateway must have signed source updates enabled in its setup. This sends the full saved source list, reloads source polling, and keeps previous settings for rollback. It does not change the broker, identity, discovery or command permissions.</p>
 {ready&&<><p>{ready.source_names.length} saved source(s) will be sent:</p><ul>{ready.source_names.map((n,i)=><li key={i}>{n}</li>)}</ul>{!ready.server_ready&&<p role="alert">{ready.reason}</p>}</>}
 <label style={{display:'flex',gap:8,alignItems:'start'}}><input style={{width:'auto',boxShadow:'none'}} type="checkbox" checked={review} onChange={e=>setReview(e.target.checked)}/>I reviewed this gateway's saved sources and allowed signed source updates in edge setup.</label>
 <button type="button" disabled={!ready?.server_ready||!review||busy||!!open||uncertain||attempt.current||!!error} onClick={()=>void send()}>{busy?'Sending...':'Apply saved source settings'}</button>
 {error&&<p role="alert">{error}</p>}
 {current&&<p role="status">{!matches&&['confirmed','applied'].includes(current.status)?'A previous request used a different source list. It does not confirm these saved settings.':messages[current.status]??'Unknown application state. Do not assume the settings are active.'}</p>}
 {current?.detail&&<details><summary>Gateway response</summary><p>{current.detail}</p></details>}
 {uncertain&&<p>Reload this saved setup to recover the latest request status. Do not create another gateway.</p>}
 </section>
}
