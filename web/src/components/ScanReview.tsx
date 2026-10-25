import {useEffect,useRef} from 'react';
export interface ScanEnrollment {gateway:string;gatewayLabel:string;scan:string;kind:string;key:string;profile:string;profileLabel:string;name:string;connection:Record<string,unknown>}
export default function ScanReview({item,busy,error,onCancel,onConfirm}:{item:ScanEnrollment;busy:boolean;error:string;onCancel:()=>void;onConfirm:()=>void}){
 const ref=useRef<HTMLDialogElement>(null);
 useEffect(()=>{const d=ref.current;const previous=document.activeElement as HTMLElement|null;d?.showModal();return()=>{d?.close();previous?.focus()}},[]);
 return <dialog ref={ref} aria-labelledby="scan-review-title" onCancel={e=>{e.preventDefault();if(!busy)onCancel()}} style={{width:'min(560px, calc(100vw - 32px))',maxHeight:'calc(100dvh - 40px)',overflowY:'auto',boxSizing:'border-box',border:'1px solid var(--line)',borderRadius:18,padding:24,color:'var(--text)',background:'var(--bg)',overflowWrap:'anywhere'}}>
  <h2 id="scan-review-title">Review device enrollment</h2>
  <p>Creates a device record only. Polling starts after you download or push the updated gateway config, not when you confirm here.</p>
  <dl><dt>Gateway</dt><dd>{item.gatewayLabel}</dd><dt>Scan transport</dt><dd>{item.kind}</dd><dt>Device name</dt><dd>{item.name}</dd><dt>Profile</dt><dd>{item.profileLabel}</dd></dl>
  <p className="muted">A scan response does not verify model compatibility or point mapping. Check the profile against the device before confirming.</p>
  <b>Connection to save</b>
  <dl>{Object.entries(item.connection).map(([k,v])=><div key={k} style={{display:'flex',justifyContent:'space-between',gap:16,padding:'6px 0',borderBottom:'1px solid var(--line)'}}><dt>{k.replaceAll('_',' ')}</dt><dd style={{margin:0,textAlign:'right'}}>{String(v)}</dd></div>)}</dl>
  {error&&<p role="alert">{error}</p>}
  <div style={{display:'flex',gap:8,flexWrap:'wrap',marginTop:16}}><button autoFocus className="ghost" disabled={busy} onClick={onCancel}>Cancel</button><button disabled={busy} onClick={onConfirm}>{busy?'Creating device...':'Confirm enrollment'}</button></div>
 </dialog>
}
