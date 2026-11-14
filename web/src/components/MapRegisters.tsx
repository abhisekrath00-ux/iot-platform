import {useEffect,useRef,useState} from 'react';
import {api} from '../lib/api';
import {MapPoint,validateMap} from '../lib/registerMap';
import {decodeRegister,labelledPoint,newRegisterLabel,RegisterLabel} from '../lib/registerPreview';
import SerialPortPicker from './SerialPortPicker';
export interface MappedProfile {id:string;name:string;driver_profile:string;points:MapPoint[];connection_defaults:Record<string,unknown>}
export type MapConnection={port:string;baud:number;data_bits:number;stop_bits:number;parity:string;address:number;interval_seconds:number;host:string;net_port:number};
type Range={transport:string;listed:string;base:string;count:string;func:string};
type Snapshot={key:string;start:number;func:number;words:number[];at:string};
export default function MapRegisters({session,gateway,onSaved}:{session:string;gateway:string;onSaved:(profile:MappedProfile,connection:MapConnection)=>void}){
 const [range,setRange]=useState<Range>({transport:'',listed:'',base:'',count:'8',func:''});
 const [connection,setConnection]=useState<MapConnection>({port:'',baud:9600,data_bits:8,stop_bits:1,parity:'none',address:1,interval_seconds:10,host:'',net_port:502});
 const [snapshot,setSnapshot]=useState<Snapshot|null>(null),[labels,setLabels]=useState<RegisterLabel[]>([]),[name,setName]=useState(''),[manual,setManual]=useState(''),[review,setReview]=useState(false);
 const [waiting,setWaiting]=useState<{id:string;key:string;start:number;func:number;count:number}|null>(null),[busy,setBusy]=useState(false),[error,setError]=useState(''),[uncertain,setUncertain]=useState(false),[saved,setSaved]=useState(false);
 const sendLock=useRef(false),saveLock=useRef(false);
 const start=range.listed.trim()===''?NaN:Number(range.listed)-(range.base==='one'?1:0),count=Number(range.count),fn=Number(range.func);
 const {interval_seconds:unusedInterval,...wireConnection}=connection;void unusedInterval;
 const key=JSON.stringify([range,wireConnection]);
 const active=snapshot?.key===key;
 const serial=range.transport==='rtu';
 const connOK=range.transport!==''&&Number.isInteger(connection.address)&&connection.address>=1&&connection.address<=247&&Number.isInteger(connection.interval_seconds)&&connection.interval_seconds>=1&&connection.interval_seconds<=86400&&(serial?/^(COM[1-9][0-9]{0,2}|\/dev\/tty[A-Za-z0-9._-]{1,60})$/.test(connection.port):/^[A-Za-z0-9][A-Za-z0-9.:-]{0,252}$/.test(connection.host)&&Number.isInteger(connection.net_port)&&connection.net_port>=1&&connection.net_port<=65535);
 const readOK=connOK&&!!range.base&&Number.isInteger(start)&&start>=0&&Number.isInteger(count)&&count>=1&&count<=32&&start+count<=65536&&[1,2,3,4].includes(fn);
 function updateRange(k:keyof Range,v:string){setRange(r=>({...r,[k]:v}));setReview(false);}
 function updateConn(k:keyof MapConnection,v:string|number){setConnection(c=>({...c,[k]:v}));setReview(false);}
 function edit(i:number,k:keyof RegisterLabel,v:string|boolean){setLabels(rs=>rs.map((r,n)=>i===n?{...r,[k]:v}:r));setReview(false);}
 useEffect(()=>{if(!waiting)return;let active=true,inflight=false;const deadline=Date.now()+20000;
  const poll=async()=>{if(inflight)return;inflight=true;try{
   const r=await api<{port_test?:{config?:{request_id?:string};result?:{request_id?:string;ok:boolean;error?:string;finished_at?:string;readings?:{point_id:string;value:number}[]}}}>(`/v1/commissioning/sessions/${encodeURIComponent(session)}`);
   if(!active)return;const test=r.port_test;
   if(test?.config?.request_id&&test.config.request_id!==waiting.id){setError('Another test replaced this read. No values were accepted.');setWaiting(null);return;}
   if(test?.result?.request_id===waiting.id){
    if(!test.result.ok){setError(test.result.error||'The gateway could not read this range. Try a smaller manual-supported range.');setWaiting(null);return;}
    const readings=test.result.readings,words=readings?.map(p=>p.value)??[];
    if(readings?.length!==waiting.count||readings.some((p,i)=>p.point_id!==`r${waiting.start+i}`||!Number.isInteger(p.value)||p.value<0||p.value>(waiting.func<=2?1:65535))||!test.result.finished_at){setError('The matching reply did not contain the complete requested raw range. No values were accepted.');setWaiting(null);return;}
    setSnapshot({key:waiting.key,start:waiting.start,func:waiting.func,words,at:test.result.finished_at});setLabels(words.map(()=>newRegisterLabel()));setWaiting(null);return;
   }
   if(Date.now()>deadline){setError('No matching reply arrived. Check the saved request and edge version. Older edge agents cannot identify this read. No automatic retry was sent.');setWaiting(null);}
  }catch{if(active){setError('Could not check the saved read result. No values were accepted and no automatic retry was sent.');setWaiting(null);}}finally{inflight=false;}};
  void poll();const timer=setInterval(()=>void poll(),2000);return()=>{active=false;clearInterval(timer)};
 },[waiting,session]);
 async function read(){if(sendLock.current||waiting||!readOK||uncertain||saved)return;if(labels.some(l=>l.include)&&!window.confirm('Read again and discard these unsaved labels?'))return;sendLock.current=true;setBusy(true);setError('');setReview(false);setSnapshot(null);setLabels([]);
  try{const r=await api<{request_id?:string}>(`/v1/commissioning/sessions/${encodeURIComponent(session)}/port-test`,{method:'POST',body:JSON.stringify({...connection,transport:range.transport,raw_range:true,func:fn,register:start,count,type:fn<=2?'bool':'u16',word_order:'abcd',timeout_ms:5000})});if(!r.request_id)throw Error('missing request identity');setWaiting({id:r.request_id,key,start,func:fn,count});}
  catch{setUncertain(true);setError('Could not confirm whether the read was sent. Reload this saved session and check its test status before sending another request. No retry was sent.');}
  finally{sendLock.current=false;setBusy(false)}
 }
 const selected=labels.map((l,i)=>({label:l,index:i,point:labelledPoint(l,(snapshot?.start??0)+i,snapshot?.func??0)})).filter(x=>x.label.include);
 const issues=validateMap(selected.map(x=>x.point)).map(x=>x.text);
 if(!connOK)issues.push('Enter valid connection settings and a poll interval of 1-86400 seconds.');
 if(!name.trim()||name.length>128)issues.push('Enter a template name of 1-128 characters.');
 if(!manual.trim()||manual.length>200)issues.push('Enter the manual/register-map reference (up to 200 characters).');
 if(selected.some(({label})=>!label.name.trim()||label.name.length>128||label.unit.length>32))issues.push('Each included point needs a name up to 128 characters and unit up to 32.');
 if(selected.some(({index,point})=>{const v=decodeRegister(snapshot?.words??[],index,point.type,point.word_order)*point.scale;return !Number.isFinite(v)||v<point.min||v>point.max}))issues.push('Decoded values must be finite, use complete returned words and fall inside the accepted range.');
 async function save(){if(saveLock.current||saved||uncertain||!review||!active||issues.length)return;saveLock.current=true;setBusy(true);setError('');
  const body={name:name.trim(),driver_profile:serial?'modbus-generic':'modbus-tcp',connection_defaults:serial?{baud:connection.baud,data_bits:connection.data_bits,stop_bits:connection.stop_bits,parity:connection.parity}:{},points:selected.map(x=>({...x.point,description:`Register map: ${manual.trim()}`}))};
  try{const r=await api<{id:string}>('/v1/profiles',{method:'POST',body:JSON.stringify(body)});setSaved(true);onSaved({...body,id:r.id},connection);}
  catch{setUncertain(true);setError('Could not confirm the template save. Check Profiles before saving again; no automatic retry was sent.');}
  finally{setBusy(false)}
 }
 return <section className="card" aria-label="Map registers" style={{marginBottom:16}}><h2>Map registers</h2>
 <p>Connect the device, read a small register range, then label the returned data and save one reusable template. No profile is needed before the read. We never guess register meanings or send device writes.</p>
 <fieldset style={{minWidth:0,boxSizing:"border-box",width:"100%"}} disabled={busy||!!waiting||saved||uncertain}><legend>1. Connect and read</legend>
 <label>Modbus connection<select aria-label="Map transport" value={range.transport} onChange={e=>updateRange('transport',e.target.value)}><option value="">Choose...</option><option value="rtu">RS-485 / USB serial (RTU)</option><option value="tcp">Network (TCP)</option></select></label>
 {serial?<><SerialPortPicker gateway={gateway} value={connection.port} onChange={v=>updateConn('port',v)}/><label>Serial port<input aria-label="Map serial port" value={connection.port} onChange={e=>updateConn('port',e.target.value)} placeholder="COM3 or /dev/ttyUSB0"/></label>
 <div style={{display:'grid',gridTemplateColumns:'repeat(auto-fit,minmax(140px,1fr))',gap:12}}><label>Baud<select aria-label="Map baud" value={connection.baud} onChange={e=>updateConn('baud',+e.target.value)}>{[1200,2400,4800,9600,19200,38400,57600,115200].map(v=><option key={v}>{v}</option>)}</select></label><label>Parity<select aria-label="Map parity" value={connection.parity} onChange={e=>updateConn('parity',e.target.value)}>{['none','even','odd'].map(v=><option key={v}>{v}</option>)}</select></label><label>Stop bits<select aria-label="Map stop bits" value={connection.stop_bits} onChange={e=>updateConn('stop_bits',+e.target.value)}><option>1</option><option>2</option></select></label></div><p>8 data bits. Check framing against the device.</p></>:range.transport==='tcp'?<><label>Device host / IP<input aria-label="Map host" value={connection.host} onChange={e=>updateConn('host',e.target.value)}/></label><label>TCP port<input aria-label="Map TCP port" type="number" value={connection.net_port} onChange={e=>updateConn('net_port',+e.target.value)}/></label></>:null}
 <div style={{display:'grid',gridTemplateColumns:'repeat(auto-fit,minmax(170px,1fr))',gap:12}}><label>Unit / slave address<input aria-label="Map slave address" type="number" min={1} max={247} value={connection.address} onChange={e=>updateConn('address',+e.target.value)}/></label>
 <label>Read function<select aria-label="Map function" value={range.func} onChange={e=>updateRange('func',e.target.value)}><option value="">Choose...</option>{[[1,'Coils'],[2,'Discrete inputs'],[3,'Holding registers'],[4,'Input registers']].map(([v,n])=><option key={v} value={v}>FC0{v}: {n}</option>)}</select></label>
 <label>Starting address in manual<input aria-label="Map listed address" type="number" value={range.listed} onChange={e=>updateRange('listed',e.target.value)}/></label>
 <label>Address convention<select aria-label="Map address convention" value={range.base} onChange={e=>updateRange('base',e.target.value)}><option value="">Choose explicitly...</option><option value="zero">Listed number is wire address</option><option value="one">Listed number minus 1 is wire address</option></select></label>
 <label>Number of registers / bits (1-32)<input aria-label="Map count" type="number" min={1} max={32} value={range.count} onChange={e=>updateRange('count',e.target.value)}/></label></div>
 <p>Wire range: {readOK?`${start}-${start+count-1}`:'choose valid connection and range'}. Reads only this range once, not a device-wide scan. An unsupported range can return an error. Only read the device you are authorized to test.</p>
 <button type="button" disabled={!readOK||busy||!!waiting||saved||uncertain} onClick={()=>void read()}>Read register range (read only)</button></fieldset>
 {waiting&&<p role="status">Waiting for this exact range request. No automatic retry.</p>}
 {snapshot&&<><h3>2. Label the returned registers</h3><p>Read at {snapshot.at}. Raw values are unscaled wire words, not identified measurements. Use the manual to choose which rows to include. A 32-bit value uses this row and the next returned register. Labels are unsaved until you save the template; keep this page open.</p>
 {!active&&<p role="alert">Connection or range changed. Read again before saving. These values belong to the previous request.</p>}
 <fieldset style={{minWidth:0,boxSizing:"border-box",width:"100%"}} disabled={busy||saved||uncertain||!active}><legend>Returned data and mapping</legend>
 <div style={{display:'grid',gridTemplateColumns:'repeat(auto-fit,minmax(220px,1fr))',gap:12}}><label>Template name<input aria-label="Map template name" value={name} onChange={e=>{setName(e.target.value);setReview(false)}}/></label><label>Manual / register-map reference<input aria-label="Map manual reference" maxLength={200} value={manual} onChange={e=>{setManual(e.target.value);setReview(false)}} placeholder="Model, revision, page"/></label><label>Poll interval (seconds)<input aria-label="Map interval" type="number" value={connection.interval_seconds} onChange={e=>updateConn('interval_seconds',+e.target.value)}/></label></div>
 <div style={{overflowX:'auto'}}><table aria-label="Returned Modbus registers"><thead><tr><th>Include</th><th>Wire address</th><th>Raw decimal / hex</th></tr></thead><tbody>{snapshot.words.map((raw,i)=>{const l=labels[i];if(!l)return null;return <tr key={i}><td><input aria-label={`Include register ${snapshot.start+i}`} type="checkbox" style={{width:'auto'}} checked={l.include} onChange={e=>edit(i,'include',e.target.checked)}/></td><td>{snapshot.start+i}</td><td>{raw} / 0x{raw.toString(16).padStart(4,'0')}</td></tr>})}</tbody></table></div>
 {labels.map((l,i)=>l.include&&<fieldset style={{minWidth:0,margin:'16px 0'}} key={i}><legend>Wire register {snapshot.start+i}: {l.name||'add meaning'}</legend>{(()=>{const value=decodeRegister(snapshot.words,i,l.type,l.order)*Number(l.scale);return <div style={{display:'grid',gridTemplateColumns:'repeat(auto-fit,minmax(140px,1fr))',gap:10,minWidth:0}}>
 {([['name','Friendly name'],['id','Stable point ID'],['unit','Unit'],['scale','Scale multiplier'],['min','Minimum'],['max','Maximum']] as const).map(([k,title])=><label key={k}>{title}<input aria-label={`Register ${snapshot.start+i} ${k}`} type={['scale','min','max'].includes(k)?'number':'text'} step="any" value={l[k]} onChange={e=>edit(i,k,e.target.value)}/></label>)}
 <label>Value type<select aria-label={`Register ${snapshot.start+i} type`} value={l.type} onChange={e=>edit(i,'type',e.target.value)}><option value="">Choose from manual...</option>{(snapshot.func<=2?['bool']:['u16','i16','u32','i32','f32']).map(v=><option key={v}>{v}</option>)}</select></label>
 <label>Word / byte order<select aria-label={`Register ${snapshot.start+i} order`} value={l.order} onChange={e=>edit(i,'order',e.target.value)}><option value="">Choose from manual...</option>{['abcd','badc','cdab','dcba'].map(v=><option key={v}>{v}</option>)}</select></label>
 <p>Converted preview: {Number.isFinite(value)?value:'choose a complete decoder'} {l.unit}. Value = decoded words × multiplier; no offset.</p></div>})()}</fieldset>)}

 <details><summary>Mapping checks ({selected.length} included point(s))</summary>{issues.length?<ul>{issues.map((v,i)=><li key={i}>{v}</li>)}</ul>:<p>IDs, ranges, decoding and returned word coverage checked. Meanings remain your manual-backed choices.</p>}</details>
 <label style={{display:'flex',gap:8,alignItems:'start'}}><input aria-label="Map review" type="checkbox" style={{width:'auto'}} checked={review} onChange={e=>setReview(e.target.checked)}/>I reviewed the included names, addresses, decoding, units and conversion against the manual.</label>
 <button type="button" disabled={!active||!review||!!issues.length||busy||saved||uncertain} onClick={()=>void save()}>Save and select reusable template</button></fieldset>
 <p>Saving creates a reusable profile only. Assign it to the source, then review and Apply saved source settings separately. No command permissions or device register writes are enabled.</p></>}
 {saved&&<p role="status">Template saved and selected. Review the source name and connection, then assign it. Nothing has been applied to the gateway.</p>}{error&&<p role="alert">{error}</p>}
 </section>;
}
