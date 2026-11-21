import {useRef,useState} from 'react';
import {api} from '../lib/api';
interface PendingPairing{id:string;serial:string;gateway_id:string;key_sha256:string;csr_sha256:string;state:string;expires_at:string}
export default function SecurePairing(){
 const [requests,setRequests]=useState<PendingPairing[]>([]),[loaded,setLoaded]=useState(false),[message,setMessage]=useState(''),[busy,setBusy]=useState(''),[compared,setCompared]=useState<Record<string,boolean>>({});
 const inFlight=useRef(false),attempted=useRef(new Set<string>());
 const [unknown,setUnknown]=useState<Set<string>>(new Set());
 async function load(){
  if(inFlight.current)return;
  inFlight.current=true;setBusy('load');setMessage('');setLoaded(false);setRequests([]);setCompared({});
  try{setRequests(await api<PendingPairing[]>('/v1/edge/identity-migrations'));setLoaded(true);}
  catch{setMessage('Could not load secure pairing requests. An interactive workspace administrator and verified HTTPS server connection are required. No saved request can be approved from this failed read.');}
  finally{inFlight.current=false;setBusy('');}
 }
 async function decide(p:PendingPairing,action:'approve'|'revoke'){
  if(inFlight.current||attempted.current.has(p.id)||(action==='approve'&&!compared[p.id]))return;
  inFlight.current=true;attempted.current.add(p.id);setBusy(p.id);setMessage('');setCompared(c=>({...c,[p.id]:false}));
  try{
   const result=await api<{broker_acl_state?:string}>(`/v1/edge/identity-migrations/${encodeURIComponent(p.id)}/${action}`,{method:'POST',body:JSON.stringify({key_sha256:p.key_sha256,csr_sha256:p.csr_sha256,compared_local_fingerprint:compared[p.id]===true})});
   setRequests(rows=>rows.map(r=>r.id===p.id?{...r,state:action==='approve'?'issued':'revoked'}:r));
   setMessage(action==='approve'?(result?.broker_acl_state==='acl_unavailable_retry_finish'?'Certificate issued, but the server ACL refresh needs attention. Keep the edge service stopped and retry Finish secure pairing after the server is checked. Issued does not mean connected.':result?.broker_acl_state==='files_written_waiting_for_broker_proof'?'Certificate issued for the compared key. Use Finish secure pairing on the stopped edge manager. It sends only a diagnostic nonce and waits for the server to observe this gateway through the secure broker before installing certificate files. No device command or meter read is sent. Issued does not mean connected.':'Certificate issued for the compared key. This server still needs its secure broker ACL/reload setup. Finish secure pairing will require a fresh server-observed broker proof before installing certificate files. Issued does not mean connected.'):'Pending request revoked. Saved gateway key and identity were not replaced.');
  }catch{setUnknown(current=>new Set(current).add(p.id));}
  finally{inFlight.current=false;setBusy('');}
 }
 return <section className="card" aria-label="Secure identity pairing" style={{marginBottom:24}}>
  <h2>Keep an existing gateway identity and add secure pairing</h2>
  <p>For a saved gateway without a client certificate, use Request secure pairing in its local edge manager. The private key stays on that PC. Compare its displayed public-key fingerprint here before approving.</p>
  <p className="muted">The server must already have a verified HTTPS endpoint, reachable authenticated TLS broker, configured device CA and broker trust bundle. This page does not expose the lab broker or create a CA, account or replacement claim.</p>
  <button type="button" disabled={!!busy} onClick={()=>void load()}>{busy==='load'?'Loading pairing requests...':loaded||unknown.size?'Reload pairing requests':'Load pairing requests'}</button>
  {message&&<p role="status">{message}</p>}
  {unknown.size>0&&<p role="alert">A pairing decision response was not confirmed. Reload only reads the latest unexpired requests; it does not resend approval or revocation. Missing or pending does not prove the decision failed. Keep this page open and the edge service stopped, and ask for help before another decision. This page will not repeat a decision for the same request.</p>}
  {loaded&&!requests.length&&<p>No unexpired requests found in the latest 100. A missing request is not proof that a previous decision failed.</p>}
  {requests.map(p=><article key={p.id} style={{marginTop:16,borderTop:'1px solid var(--line)',paddingTop:12}}>
   <h3>Gateway {p.serial}</h3>
   <p style={{overflowWrap:'anywhere'}}>Request {p.id}. Status: {p.state}. Expires {new Date(p.expires_at).toLocaleString()}.</p>
   {unknown.has(p.id)&&<p>Decision response unknown. This status is the latest observed request state, not a confirmed response to that decision.</p>}
   {p.state==='pending'&&attempted.current.has(p.id)&&!unknown.has(p.id)&&<p>A decision was already submitted for this request on this page. The latest read will not re-enable it. Keep the edge service stopped and check the server before another decision.</p>}
   <p>Public-key SHA256:</p><code style={{display:'block',overflowWrap:'anywhere'}}>{p.key_sha256}</code>
   <details><summary>Inspect exact request</summary><p style={{overflowWrap:'anywhere'}}>CSR SHA256: {p.csr_sha256}</p><p style={{overflowWrap:'anywhere'}}>Gateway ID: {p.gateway_id}</p></details>
   {p.state==='pending'&&<><label style={{display:'flex',gap:8,alignItems:'start'}}><input style={{width:'auto'}} type="checkbox" disabled={!!busy||attempted.current.has(p.id)} checked={!!compared[p.id]} onChange={e=>setCompared(c=>({...c,[p.id]:e.target.checked}))}/>I compared this exact public-key fingerprint with the local edge manager on the intended PC.</label>
    <div style={{display:'flex',gap:8,flexWrap:'wrap'}}><button type="button" disabled={!!busy||attempted.current.has(p.id)||!compared[p.id]} onClick={()=>void decide(p,'approve')}>Approve this key</button><button type="button" className="secondary" disabled={!!busy||attempted.current.has(p.id)} onClick={()=>void decide(p,'revoke')}>Revoke pending request</button></div>
   </>}
  </article>)}
 </section>;
}
