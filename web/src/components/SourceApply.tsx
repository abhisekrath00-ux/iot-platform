import {useEffect,useRef,useState} from 'react';
import {api} from '../lib/api';
import {loadAttemptJournal,recordAttempt,releaseAcknowledgedApply,AttemptJournal} from '../lib/attemptJournal';
interface Push {id:string;status:string;detail?:string;version:number;sha256?:string;publication_unconfirmed?:boolean;recovery_quarantined?:boolean;}
interface Ready {server_ready:boolean;reason:string;desired_sha256:string;source_names:string[];}
interface Me {role:string;via_key?:boolean;customer_id?:string;}
const messages:Record<string,string>={sent:'Sent to gateway. Waiting for its answer.',applied:'Gateway saved the settings. Waiting for its connection check after reload.',confirmed:'Gateway confirmed the settings after reload. Check a live source reading next.',rejected:'Gateway did not confirm applying these settings. Review its response before retrying.',rolled_back:'Gateway restored its previous settings after the connection check failed.',failed:'Settings could not be sent.',expired:'No gateway answer arrived before this request expired.'};
export default function SourceApply({gateway}:{gateway:string}){
 const [ready,setReady]=useState<Ready|null>(null);
 const [me,setMe]=useState<Me|null>(null),[pushes,setPushes]=useState<Push[]>([]),[error,setError]=useState(''),[busy,setBusy]=useState(false),[reviewedKey,setReviewedKey]=useState(''),[uncertain,setUncertain]=useState(false);
 const attempt=useRef(false),generation=useRef(0),journal=useRef<AttemptJournal|null>(null);
 const [journalGateway,setJournalGateway]=useState(''),[lockError,setLockError]=useState(false),[restored,setRestored]=useState(false);
 const [loadedGateway,setLoadedGateway]=useState('');
 const reviewKey=ready?JSON.stringify([gateway,ready.desired_sha256,ready.source_names]):'';
 const review=loadedGateway===gateway&&!!reviewedKey&&reviewedKey===reviewKey;
 useEffect(()=>{let active=true;api<Me>('/v1/me').then(x=>{if(active)setMe(x)}).catch(()=>{});return()=>{active=false}},[]);
 const allowed=me?.role==='admin'&&!me.via_key&&!me.customer_id;
 useEffect(()=>{if(!allowed)return;let active=true;journal.current=null;setJournalGateway('');setLockError(false);setRestored(false);setUncertain(false);setBusy(false);attempt.current=false;
  loadAttemptJournal('apply').then(j=>{if(!active)return;journal.current=j;setJournalGateway(gateway);setRestored(j.ids.has(gateway));setUncertain(j.ids.has(gateway));}).catch(()=>{if(active)setLockError(true)});
  const load=()=>{const seq=++generation.current;return Promise.all([api<Push[]>(`/v1/gateways/${encodeURIComponent(gateway)}/config-push`),api<Ready>(`/v1/gateways/${encodeURIComponent(gateway)}/source-readiness`)]).then(([x,r])=>{if(active&&seq===generation.current){setPushes(x);setReady(r);setLoadedGateway(gateway);setError('')}}).catch(()=>{if(active)setError('Could not check gateway application status. Do not send another request until status can be checked.');});};
  load();const t=setInterval(load,5000);return()=>{active=false;clearInterval(t)};
 },[gateway,allowed]);
 async function send(){if(journalGateway!==gateway||!journal.current||lockError||uncertain||loadedGateway!==gateway||attempt.current||!review||!ready?.server_ready||error||pushes.some(p=>p.recovery_quarantined||p.publication_unconfirmed||['sent','applied'].includes(p.status)))return;attempt.current=true;++generation.current;setBusy(true);setError('');
  const submittedJournal=journal.current;
  try{recordAttempt(submittedJournal,gateway);}catch{setLockError(true);setBusy(false);return;}
  try{const r=await api<{push_id:string;version:number;status:string;sha256?:string}>(`/v1/gateways/${encodeURIComponent(gateway)}/config-push`,{method:'POST',body:JSON.stringify({expected_sha256:ready.desired_sha256})});if(typeof r.push_id!=='string'||!r.push_id||r.push_id.length>80||!Number.isInteger(r.version)||r.version<1||r.sha256!==ready.desired_sha256||!['sent','applied','confirmed','rejected','rolled_back','failed','expired'].includes(r.status))throw Error('mismatched application receipt');try{releaseAcknowledgedApply(submittedJournal,gateway);}catch{if(journal.current===submittedJournal)setLockError(true);}if(journal.current!==submittedJournal)return;++generation.current;setPushes([{id:r.push_id,version:r.version,status:r.status,sha256:r.sha256}]);}
  catch{if(journal.current!==submittedJournal)return;setUncertain(true);setError('Could not confirm whether this request was sent. Check its saved status before trying again. No automatic retry was sent.');}
  finally{if(journal.current===submittedJournal)setBusy(false)}
 }
 if(!allowed)return <p className="muted">A workspace admin can apply saved sources to the gateway. Saving a source does not apply it.</p>;
 if(loadedGateway!==gateway||journalGateway!==gateway)return <section className="card" aria-label="Apply source settings"><h3>Apply saved sources to this gateway</h3><p role={error||lockError?'alert':'status'}>{lockError?'This tab could not safely retain the Apply attempt history. No new Apply is allowed. Do not clear storage or sign in again to retry. Ask an administrator to inspect the saved request.':error||'Checking saved sources and application status for this gateway. No request has been sent.'}</p><button type="button" disabled>Apply saved source settings</button></section>;
 const current=pushes[0],matches=!!current&&current.sha256===ready?.desired_sha256,open=pushes.some(p=>p.recovery_quarantined||p.publication_unconfirmed||['sent','applied'].includes(p.status));
 return <section className="card" aria-label="Apply source settings" style={{margin:'16px 0'}}><h3>Apply saved sources to this gateway</h3>
 <p>The gateway must have signed source updates enabled in its setup. This sends the full saved source list, reloads source polling, and keeps previous settings for rollback. It does not change the broker, identity, discovery or command permissions.</p>
 {ready&&<><p>{ready.source_names.length} saved source(s) will be sent:</p><ul>{ready.source_names.map((n,i)=><li key={i}>{n}</li>)}</ul>{!ready.server_ready&&<p role="alert">{ready.reason}</p>}</>}
 <label style={{display:'flex',gap:8,alignItems:'start'}}><input style={{width:'auto',boxShadow:'none'}} type="checkbox" checked={review} disabled={lockError||!ready||busy||uncertain||attempt.current||pushes.some(p=>p.publication_unconfirmed||p.recovery_quarantined)} onChange={e=>setReviewedKey(e.target.checked?reviewKey:'')}/>I reviewed this gateway's saved sources and allowed signed source updates in edge setup.</label>
 <button type="button" disabled={lockError||!ready?.server_ready||!review||busy||!!open||uncertain||attempt.current||!!error} onClick={()=>void send()}>{busy?'Sending...':'Apply saved source settings'}</button>
 {lockError&&<p role="alert">This tab could not safely retain the Apply attempt history. No new Apply is allowed. Do not clear storage or sign in again to retry. Ask an administrator to inspect the saved request.</p>}
 {restored&&<p role="alert">Previous Apply response unknown in this tab. The saved status below is an observation, not a confirmed response to that attempt. Missing or expired history does not prove that a source reload failed. No new Apply is allowed from this tab.</p>}
 {error&&<p role="alert">{error}</p>}
 {pushes.some(p=>p.recovery_quarantined)&&<p role="alert">A retained Apply safety record blocks this gateway, even if database history was restored or is missing. This record does not prove delivery or failure. No new Apply is allowed, including after a terminal gateway response. Keep the edge stopped and ask an administrator to inspect recovery. Do not delete or replace safety storage to retry.</p>}
 {current?.publication_unconfirmed&&<p role="alert">The server could not confirm the broker publication response for this saved request. A source reload may already have been accepted. No new Apply is allowed while this request is unresolved, including after reloading this page. Keep the edge stopped and ask an administrator to inspect this exact request. Do not clear or replace it to retry.</p>}
 {current&&<p role="status">{current.recovery_quarantined?'Retained Apply safety quarantine. No new request can be sent.':current.publication_unconfirmed?'Publication response unconfirmed. The saved request is quarantined; this is not a confirmed send or a confirmed failure.':!matches&&['confirmed','applied'].includes(current.status)?'A previous request used a different source list. It does not confirm these saved settings.':messages[current.status]??'Unknown application state. Do not assume the settings are active.'}</p>}
 {current?.detail&&<details><summary>Saved request detail</summary><p>{current.detail}</p></details>}
 {uncertain&&<p>Reload this saved setup only to inspect the latest saved request status, not to retry Apply. Missing from the latest 20 requests is not proof that a send failed. Do not close this tab, clear storage, sign in again or create another gateway to retry. This guard covers only this tab and the exact current sign-in, not other tabs or devices.</p>}
 </section>
}
