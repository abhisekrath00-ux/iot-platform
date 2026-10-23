import {useEffect,useState} from 'react';
import {LineChart,Line,XAxis,YAxis,Tooltip,ResponsiveContainer,CartesianGrid} from 'recharts';
import {api} from '../lib/api';
import {Widget} from '../lib/widgets';
import {formatValue} from '../lib/format';
import {chart as ct} from '../lib/theme';
export interface GroupBucket {t:string;v:number|null;devices:number;members:number;samples:number;unit:string;units_mixed:boolean}

export default function GroupSeriesWidget({w}:{w:Widget}) {
 const [rows,setRows]=useState<GroupBucket[]>([]);const [state,setState]=useState('Loading...');
 useEffect(()=>{
  let alive=true;setRows([]);setState('Loading...');
  const load=()=>api<GroupBucket[]>(`/v1/telemetry/group-series?group_id=${encodeURIComponent(w.group_id??'')}&point_id=${encodeURIComponent(w.point_id??'')}&agg=${w.agg??'sum'}`)
   .then(r=>{if(alive){setRows(r);setState(r.length?'':'No raw samples in the last 24 hours.');}})
   .catch(()=>{if(alive){setRows([]);setState('Could not load the group trend.');}});
  load();const timer=setInterval(load,60000);return()=>{alive=false;clearInterval(timer)};
 },[w.group_id,w.point_id,w.agg]);
 const mixed=rows.some(r=>r.units_mixed);
 const contributors=rows.map(r=>r.devices);const members=rows[0]?.members??0;
 // Insert null buckets for missing hours so the line never bridges a gap.
 const data:GroupBucket[]=[];
 for(const row of rows){if(data.length){for(let t=Date.parse(data[data.length-1].t)+3600000;t<Date.parse(row.t);t+=3600000)data.push({...row,t:new Date(t).toISOString(),v:null,devices:0,samples:0})}data.push(row)}
 return <div className="card" style={{minWidth:0}}>
  <div className="muted">{w.title}</div>
  <p className="muted" style={{fontSize:12}}>24 h · hourly device means, then group {w.agg??'sum'}{rows[0]?.unit?` (${rows[0].unit})`:''}</p>
  {state&&<p role="status">{state}</p>}
  {mixed?<p role="alert" className="pill warn">Mixed units in this window. Trend withheld.</p>:rows.length>0&&<>
   <div style={{width:'100%',height:220}}><ResponsiveContainer><LineChart data={data}>
    <CartesianGrid stroke={ct.grid}/><XAxis dataKey="t" stroke={ct.axis} fontSize={11} tickFormatter={t=>new Date(t).toLocaleTimeString([],{hour:'2-digit',minute:'2-digit'})}/>
    <YAxis stroke={ct.axis} fontSize={11} domain={['auto','auto']} tickFormatter={formatValue} width={56}/>
    <Tooltip contentStyle={ct.tooltip} labelFormatter={t=>new Date(String(t)).toLocaleString()}/>
    <Line type="linear" dataKey="v" name={`${w.agg??'sum'} of device means`} stroke={ct.line} dot={{r:2}} connectNulls={false} strokeWidth={2}/>
   </LineChart></ResponsiveContainer></div>
   <p className="muted" style={{fontSize:12}}>{Math.min(...contributors)}-{Math.max(...contributors)} of {members} current member devices contribute per populated hour. Missing devices are omitted, not zero.</p>
  </>}
  <p className="muted" style={{fontSize:12}}>Current group tree and customer scope, not historical membership. Raw samples only; retained rollups are not used because they carry no units. First and current hours can be partial. Refreshes each minute.</p>
 </div>
}
