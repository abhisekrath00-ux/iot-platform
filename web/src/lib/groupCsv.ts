// The existing API enforces tenant/customer scope and mixed-unit withholding.
// A fresh download is deliberately not described as the displayed snapshot.
export async function downloadGroupCSV(group:string,point:string,agg:string):Promise<void>{
 const token=localStorage.getItem('iot.token')??'';
 const q=new URLSearchParams({group_id:group,point_id:point,agg,format:'csv'});
 const r=await fetch(`/v1/telemetry/group-series?${q}`,{headers:token?{Authorization:`Bearer ${token}`}:{}});
 if(!r.ok){if(r.status===409)throw Error('Mixed units in the fresh window. CSV withheld.');if(r.status===401)throw Error('Sign in again before downloading.');throw Error('Could not download group CSV. Try again.');}
 if(!r.headers.get('content-type')?.toLowerCase().startsWith('text/csv'))throw Error('Unexpected download response. No file saved.');
 const url=URL.createObjectURL(await r.blob());const a=document.createElement('a');a.href=url;a.download='group-series-24h.csv';document.body.appendChild(a);a.click();a.remove();setTimeout(()=>URL.revokeObjectURL(url),1000);
}
