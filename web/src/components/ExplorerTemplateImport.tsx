import {useRef,useState} from 'react';
import {api} from '../lib/api';
import {ImportedProfile,MapPoint,parseAny} from '../lib/registerMap';

export interface ExplorerProfile {id:string;name:string;driver_profile:string;points:MapPoint[];connection_defaults:Record<string,unknown>;}
interface Draft {profile:ImportedProfile;points:MapPoint[];}

// Portable annotations only. No endpoint, gateway, session, probe, source
// assignment, command grant or Apply is taken from an imported file.
export default function ExplorerTemplateImport({onSaved}:{onSaved:(profile:ExplorerProfile)=>void}){
 const [draft,setDraft]=useState<Draft|null>(null),[review,setReview]=useState(false),[error,setError]=useState(''),[busy,setBusy]=useState(false),[saved,setSaved]=useState(false),[uncertain,setUncertain]=useState(false);
 const generation=useRef(0),attempt=useRef(false);
 async function importFile(file:File|undefined){
  if(!file||attempt.current)return;const seq=++generation.current;setReview(false);setError('');setDraft(null);
  if(file.size>262144){setError('Template exceeds 256 KiB. Nothing was saved or selected.');return;}
  try{
   const parsed=parseAny(await file.text());if(seq!==generation.current)return;
   if(parsed.issues.length||!parsed.profile||!parsed.points.length){setError(parsed.issues.map(x=>x.text).join(' ')||'Choose a versioned HexThings explorer source-template JSON, not a label file or raw register map. Nothing was saved.');return;}
   setDraft({profile:parsed.profile,points:parsed.points});
  }catch{if(seq===generation.current)setError('Could not read this file. Nothing was saved or selected.');}
 }
 async function save(){
  if(attempt.current||busy||!draft||!review||saved||uncertain)return;attempt.current=true;setBusy(true);setError('');
  const body={name:draft.profile.name,driver_profile:draft.profile.driver_profile,connection_defaults:{},points:draft.points};
  try{
   const result=await api<{id:string}>('/v1/profiles',{method:'POST',body:JSON.stringify(body)});
   if(typeof result.id!=='string'||!result.id||result.id.length>80)throw Error('missing profile receipt');
   setSaved(true);onSaved({...body,id:result.id});
  }catch{setUncertain(true);setError('Could not confirm whether the template was saved. Check Profiles and reload this saved setup before creating another profile. No automatic retry was sent.');}
  finally{setBusy(false);}
 }
 return <section className="card" aria-label="Import edge explorer template" style={{marginBottom:16}}>
 <h2>Use a template from the edge explorer</h2>
 <p>Export the reviewed source template in the local edge explorer, then import that JSON here. Review it before saving one reusable profile. Importing never selects a gateway, sends a test, assigns a source or applies settings.</p>
 <label>Reviewed explorer template JSON<input aria-label="Explorer template file" type="file" accept="application/json,.json" disabled={busy||saved||uncertain} onChange={e=>{const file=e.target.files?.[0];e.target.value='';void importFile(file)}}/></label>
 {draft&&<><h3>{draft.profile.name}</h3><p>{draft.profile.driver_profile} | {draft.points.length} point(s). No saved connection defaults. Enter the actual gateway connection separately below.</p>
 <div style={{display:'grid',gridTemplateColumns:'repeat(auto-fit,minmax(min(100%,240px),1fr))',gap:12}}>{draft.points.map(p=><fieldset key={p.id} style={{minWidth:0,overflowWrap:'anywhere'}}><legend>{p.name} ({p.id})</legend><p>{p.description}</p><dl><dt>Wire offset / function</dt><dd>{p.register} / FC{p.func}</dd><dt>Decoding / word order</dt><dd>{p.type} / {p.word_order}</dd><dt>Scale / unit</dt><dd>{p.scale} / {p.unit||'(none)'}</dd><dt>Accepted values</dt><dd>{p.min} to {p.max}</dd></dl></fieldset>)}</div>
 <label style={{display:'flex',gap:8,alignItems:'start'}}><input aria-label="Explorer import review" type="checkbox" style={{width:'auto'}} checked={review} disabled={busy||saved||uncertain} onChange={e=>setReview(e.target.checked)}/>I checked the model/manual, each point's wire address, name, decoding, unit and accepted limits for this source.</label>
 <button type="button" disabled={!review||busy||saved||uncertain} onClick={()=>void save()}>{busy?'Saving template...':'Save and select imported template'}</button></>}
 {saved&&<p role="status">Imported template saved and selected below. Enter the source name and actual connection, then Assign profile. Nothing is applied until a separate reviewed Apply request and gateway confirmation.</p>}
 {error&&<p role="alert">{error}</p>}
 </section>;
}
