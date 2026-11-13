import { useEffect, useState } from 'react';
import { api, download } from '../lib/api';
interface Package { id: string; filename: string; os: 'windows'|'linux'; arch: 'amd64'|'arm64'; format: string; size: number; sha256: string; }
interface Catalog { version: string; packages: Package[]; }
export default function EdgeDownloads() {
 const [catalog,setCatalog]=useState<Catalog|null>(null), [os,setOS]=useState<'windows'|'linux'>('windows'), [arch,setArch]=useState<'amd64'|'arm64'>('amd64');
 const [error,setError]=useState(''),[busy,setBusy]=useState(false),[loading,setLoading]=useState(true);
 async function load(){setLoading(true);setError('');try{setCatalog(await api<Catalog>('/v1/edge-packages'));}catch(e){setCatalog(null);setError(String((e as Error).message??e));}finally{setLoading(false);}}
 useEffect(()=>{void load();},[]);
 const format=os==='windows'?'exe':'run';const pkg=catalog?.packages.find(p=>p.os===os&&p.arch===arch&&p.format===format);
 async function get(p:Package){setBusy(true);setError('');try{await download('/v1/edge-packages/'+encodeURIComponent(p.id),p.filename,{size:p.size,sha256:p.sha256});}catch(e){setError(String((e as Error).message??e));}finally{setBusy(false);}}
 return <section className="card" aria-labelledby="edge-downloads-title" style={{margin:'12px 0'}}>
  <h3 id="edge-downloads-title">Download and install the edge application</h3>
  <p>Install on the PC beside your sensors, not on the server by mistake. The agent runs as an operating-system service, including when nobody is signed in. No Docker or WSL is needed.</p>
  <div style={{display:'grid',gridTemplateColumns:'repeat(auto-fit,minmax(min(100%,240px),1fr))',gap:12}}>
   <label>Edge PC operating system<select aria-label="Edge PC operating system" value={os} onChange={e=>setOS(e.target.value as 'windows'|'linux')}><option value="windows">Windows</option><option value="linux">Ubuntu / Linux with systemd</option></select></label>
   <label>Edge PC processor<select aria-label="Edge PC processor" value={arch} onChange={e=>setArch(e.target.value as 'amd64'|'arm64')}><option value="amd64">Intel / AMD 64-bit (x64)</option><option value="arm64">ARM64 (not ESP32)</option></select></label>
  </div>
  {loading?<p role="status">Checking packages bundled with this server...</p>:pkg?<>
   <p><b>{os==='windows'?'Windows setup executable (.exe)':'Ubuntu executable installer (.run)'}</b> · Version {catalog!.version} · {(pkg.size/1000000).toFixed(2)} MB</p>
   <button type="button" disabled={busy} onClick={()=>void get(pkg)}>{busy?'Downloading...':os==='windows'?'Download Windows installer':'Download Ubuntu installer'}</button>
   <p style={{overflowWrap:'anywhere',fontSize:13}}>File: <code>{pkg.filename}</code><br/>SHA256: <code>{pkg.sha256}</code></p>
   <ol>
    <li>{os==='windows'?'Run the downloaded setup EXE and approve Administrator access. It opens the installed edge setup/manager.':'Make the .run file executable, then run it with sudo. The guided setup installs the local edge manager and systemd service.'}</li>
    <li>Use Add device in this dashboard for guided sensor assignment and the full gateway YAML. Use your verified server address, one-time claim code and gateway serial to enroll. Keep the code private. Install-only is available if the connection is not ready.</li>
    <li>Import the gateway YAML generated after sensor assignment. The unchanged example config is not ready for a real sensor.</li>
    <li>Choose "Start automatically when this PC boots" during setup or in the installed manager. It is optional. A new unconfigured install stays manual; startup is applied only after real config checks.</li>
    <li>Start the service from the manager. Check local health at <code>http://127.0.0.1:8088</code> on that PC, then verify the first sensor reading in this dashboard.</li>
   </ol>
   {os==='linux'&&<p><code>chmod +x {pkg.filename}</code><br/><code>sudo ./{pkg.filename}</code><br/>After installation: <code>sudo hexthings-edge</code> for config, Start/Stop, boot startup, health and logs.</p>}
   <details><summary>Advanced packages and lifecycle details</summary>
    <p>Keep a backup before upgrades. Existing config and identity are retained. An upgrade can restart an already running service. Uninstall retains config/identity unless a separate purge is explicitly chosen.</p>
    <p>{os==='windows'?'Windows: HexmonEdge service; installed manager in the Start menu.':'Ubuntu: hexmon-edge.service; local manager command: sudo hexthings-edge.'} A running service is not proof of working sensor data. Broker-unreachable health means readings may be buffered.</p>
    {catalog!.packages.filter(p=>p.os===os&&p.arch===arch&&p.format!==format).map(p=><p key={p.id}><button type="button" className="secondary" disabled={busy} onClick={()=>void get(p)}>Download {p.format==='deb'?'Ubuntu .deb (manual startup by default)':p.format+' archive'}</button><br/><small style={{overflowWrap:'anywhere'}}>SHA256: {p.sha256}</small></p>)}
   </details>
  </>:<p role="status">No matching installer is available on this server. Rebuild the server image with bundled edge packages.</p>}
  {error&&<p role="alert">Download unavailable: {error} <button type="button" className="secondary" onClick={()=>void load()}>Retry package check</button></p>}
  <p className="muted">The browser checks the downloaded installer size and SHA256 against this inventory before saving. This detects changed bytes, not publisher identity or code signing.</p>
  <p className="muted">Packages are served by this HexThings server, not an external download site. Setup does not change server bindings, firewall rules or public exposure. A separate PC still needs a reachable secure API/MQTT connection.</p>
  <p className="muted">Acceptance limits: Windows setup is unsigned. New native setup/manager, boot behavior and live sensor connection still require target-PC acceptance. Build and contract tests are not an industrial-readiness claim.</p>
 </section>;
}
