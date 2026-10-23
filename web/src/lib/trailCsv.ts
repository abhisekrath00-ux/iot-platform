export interface TrailPoint {lat:number;lon:number;at:string;source:string}
// Quote all text and neutralize spreadsheet formula prefixes. Numeric coordinates
// remain numbers, so southern/western locations do not become text formulas.
function text(s:string):string {
  if (/^[\s\u0000-\u001f]*[=+@-]/.test(s)) s="'"+s;
  return '"'+s.replace(/"/g,'""')+'"';
}
export function trailCsv(device:string,hours:number,total:number,points:TrailPoint[]):string {
  const rows=['device_id,window_hours,available_samples,exported_samples,recorded_at,latitude,longitude,source'];
  for(const p of points) rows.push([text(device),hours,total,points.length,text(p.at),p.lat,p.lon,text(p.source)].join(','));
  return rows.join('\r\n')+'\r\n';
}
export function downloadTrailCsv(device:string,hours:number,total:number,points:TrailPoint[]):void {
  const url=URL.createObjectURL(new Blob([trailCsv(device,hours,total,points)],{type:'text/csv;charset=utf-8'}));
  const a=document.createElement('a');a.href=url;
  a.download=`trail-${device.replace(/[^a-zA-Z0-9_-]/g,'_').slice(0,64)}-${hours}h.csv`;
  document.body.appendChild(a);a.click();a.remove();
  // Keep the URL alive long enough for the browser's download handler.
  setTimeout(()=>URL.revokeObjectURL(url),1000);
}
