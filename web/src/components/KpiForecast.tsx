import {useEffect,useState} from 'react';
import {api} from '../lib/api';
interface Projection {enough_data:boolean;useful:boolean;reason?:string;complete_hours:number;backtest?:{mae:number;seasonal_naive_mae:number};forecast?:{t:string;value:number;lower:number;upper:number}[]}
export default function KpiForecast({id,unit}:{id:string;unit:string}) {
 const [open,setOpen]=useState(false);const [p,setP]=useState<Projection|null>(null);const [error,setError]=useState('');const [refresh,setRefresh]=useState(0);
 useEffect(()=>{if(!open)return;let dead=false;setP(null);setError('');api<{projection:Projection}>(`/v1/kpis/${encodeURIComponent(id)}/history?hours=336&forecast_hours=24`).then(x=>{if(!dead)setP(x.projection)}).catch(()=>{if(!dead)setError('Could not load the KPI forecast. Try Refresh forecast.')});return()=>{dead=true}},[id,open,refresh]);
 const pts=p?.forecast??[];
 const low=Math.min(...pts.map(x=>x.lower)),high=Math.max(...pts.map(x=>x.upper)),span=high-low||1;
 const x=(i:number)=>10+i*280/Math.max(1,pts.length-1),y=(v:number)=>105-(v-low)*90/span;
 return <div style={{marginTop:10}}>
  <button className="ghost" aria-expanded={open} onClick={()=>setOpen(!open)}>{open?'Hide KPI forecast':'Show KPI forecast'}</button>
  {open&&<section aria-label="KPI forecast" style={{marginTop:10}}>
   <b>Statistical KPI forecast, next 24 h</b>
   <p className="muted" style={{fontSize:12}}>Uses 14 days of complete UTC hours. Expression of hourly input averages, not average of the expression. No gap filling, alerts or control actions.</p>
   <button className="ghost" onClick={()=>setRefresh(n=>n+1)}>Refresh forecast</button>
   <div role="status">{error||(!p?'Loading KPI forecast...':!p.enough_data?`Not enough complete history (${p.complete_hours}/336 hours). ${p.reason??''}`:!p.useful?`Not shown as a prediction: ${p.reason??'backtest did not improve on repeating yesterday'}.`:'Backtest passed on the last 24 hours.')}</div>
   {p?.backtest&&<p className="muted" style={{fontSize:12}}>Mean absolute error {p.backtest.mae} vs {p.backtest.seasonal_naive_mae} for repeating yesterday. Must improve by more than 5%.</p>}
   {p?.enough_data&&p.useful&&pts.length>0&&<>
    <svg role="img" aria-label="KPI forecast with approximate interval" viewBox="0 0 300 120" width="100%" style={{display:'block'}}>
     <polygon points={[...pts.map((v,i)=>`${x(i)},${y(v.upper)}`),...pts.map((v,i)=>`${x(i)},${y(v.lower)}`).reverse()].join(' ')} fill="var(--accent,#006cce)" opacity="0.15"/>
     <polyline points={pts.map((v,i)=>`${x(i)},${y(v.value)}`).join(' ')} fill="none" stroke="var(--accent,#006cce)" strokeWidth="2" strokeDasharray="4 3"/>
    </svg>
    <p className="muted" style={{fontSize:12}}>Interval range {Number(low.toFixed(3))} to {Number(high.toFixed(3))} {unit}. Left to right: hourly steps.</p>
    <p className="muted" style={{fontSize:12}}>Approximate 95% band, not a guarantee. {new Date(pts[0].t).toLocaleString()} to {new Date(pts[pts.length-1].t).toLocaleString()}. Unit: {unit||'not specified'}.</p>
    <details><summary>Hourly forecast values and intervals</summary><div style={{overflowX:'auto'}}><table><thead><tr><th>Time</th><th>Value</th><th>Lower</th><th>Upper</th></tr></thead><tbody>{pts.map(v=><tr key={v.t}><td>{new Date(v.t).toLocaleString()}</td><td>{v.value}</td><td>{v.lower}</td><td>{v.upper}</td></tr>)}</tbody></table></div></details>
   </>}
  </section>}
 </div>
}
