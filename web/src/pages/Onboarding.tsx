import SerialPortPicker from '../components/SerialPortPicker';
import SecurePairing from '../components/SecurePairing';
import SchneiderMeterTemplate from '../components/SchneiderMeterTemplate';
import SourceApply from '../components/SourceApply';
import SetupRecovery from '../components/SetupRecovery';
import EdgeDownloads from '../components/EdgeDownloads';
import { useEffect, useRef, useState } from 'react';
import { SiteSelect } from '../components/SiteSelect';
import QRCode from 'qrcode';
import { api, download, EXTRA_DRIVERS, NETWORK_DRIVERS } from '../lib/api';

// Guided commissioning wizard: site+serial -> QR claim -> profile ->
// read-only port test -> live preview. Each step polls the session, whose
// state the API derives from live evidence.

interface ProbeSettings { port:string; baud:number; data_bits:number; stop_bits:number; parity:string; address:number; func:number; register:number; count:number; type:string; word_order:string; timeout_ms:number; }
interface Session {
  source?: {device_id:string;name:string;driver:string;connection:Record<string,any>} | null;
  session_id: string;
  gateway_id: string;
  site_id?: string;
  profile_id?: string | null;
  serial: string;
  gateway_status: string;
  device_id: string | null;
  state: string;
  claim_code?: string;
  enroll_string?: string;
  qr_payload?: string;
  port_test?: { config?: ProbeSettings; result?: { ok: boolean; error?: string; readings?: { point_id: string; value: number }[]; latency_ms?: number } } | null;
}
interface Site { id: string; name: string; }
interface Profile { points?:{register:number;func?:number;type?:string;word_order?:string}[]; connection_defaults?:Record<string,unknown>; id: string; name: string; driver_profile: string; }
interface PreviewPoint { point_id: string; value: number; unit: string; quality: string; observed_at: string; recent_measured?:boolean; }

const STEPS = ['Site & claim code', 'Gateway claim', 'Device profile', 'Port test', 'Live preview'];
const HINTS = [
  'Pick the site and name the gateway. You get a one-time claim code to enter on the gateway.',
  'Waiting for the gateway to claim itself with the code. It generates its own key and gets a certificate.',
  'Choose which sensor model and points this gateway reads. Profiles generate the edge config.',
  'Test the port from the gateway before going live. Nothing is written to the device.',
  'Confirm real readings arrive. Commissioning finishes when the first value lands.',
];

export default function Onboarding() {
  const [resumeId] = useState(() => new URLSearchParams(window.location.search).get('session') ?? '');
  const [resuming, setResuming] = useState(!!resumeId);
  const [resumeError, setResumeError] = useState('');
  const [resumeProbeReviewed, setResumeProbeReviewed] = useState(false);
  const [step, setStep] = useState(0);
  const [sites, setSites] = useState<Site[]>([]);
  const [site, setSite] = useState('');
  const [serial, setSerial] = useState('');
  const [session, setSession] = useState<Session | null>(null);
  const [profiles, setProfiles] = useState<Profile[]>([]);
  const [profileId, setProfileId] = useState('');
  const [deviceName, setDeviceName] = useState('');
  const [probe, setProbe] = useState<ProbeSettings>({ port: '/dev/ttyUSB0', baud: 9600, data_bits: 8, stop_bits: 1, parity: 'none', address: 1, func: 3, register: 0, count: 2, type: 'f32', word_order: 'abcd', timeout_ms: 3000 });
  const [conn, setConn] = useState<Record<string, any>>({ port: '', baud: 9600, data_bits:8, stop_bits:1, parity:'none', address: '', host: '', net_port: 0, endpoint: 'opc.tcp://', interval_seconds: 10 }); // eslint-disable-line @typescript-eslint/no-explicit-any
  const [previewLive,setPreviewLive]=useState(false),[previewError,setPreviewError]=useState('');
  const [preview, setPreview] = useState<PreviewPoint[]>([]);
  const [msg, setMsg] = useState('');
  const creationAttempted = useRef(false);
  const [creating, setCreating] = useState(false);
  const [creationUncertain, setCreationUncertain] = useState(false);
  const driver = session?.source?.driver ?? profiles.find(p => p.id === profileId)?.driver_profile ?? '';
  const isNet = NETWORK_DRIVERS.includes(driver);
  const qrRef = useRef<HTMLCanvasElement>(null);

  useEffect(() => {
    if (!resumeId) return;
    let active = true;
    if (!/^[A-Za-z0-9-]{1,80}$/.test(resumeId)) { setResumeError('Invalid saved session ID. No new claim was created.'); setResuming(false); return; }
    api<Session>(`/v1/commissioning/sessions/${encodeURIComponent(resumeId)}`).then(saved => {
      if (!active) return;
      if (saved.session_id !== resumeId || !saved.gateway_id || !saved.serial) throw Error('Saved session response does not match');
      if(saved.source) {
        if(saved.source.device_id !== saved.device_id) throw Error("Saved source does not match the session");
        setDeviceName(saved.source.name); setConn({...saved.source.connection});
        const c=saved.source.connection;
        setProbe(prev=>({...prev,...Object.fromEntries(["port","baud","data_bits","stop_bits","parity","address"].filter(k=>c[k]!==undefined).map(k=>[k,c[k]]))}));
      }
      if(saved.port_test?.config) { const p=saved.port_test.config; setProbe({port:p.port,baud:p.baud,data_bits:p.data_bits,stop_bits:p.stop_bits,parity:p.parity,address:p.address,func:p.func,register:p.register,count:p.count,type:p.type,word_order:p.word_order,timeout_ms:p.timeout_ms}); }
      setSession(saved); setSerial(saved.serial); setSite(saved.site_id ?? ''); setProfileId(saved.profile_id ?? '');
      setStep(saved.device_id ? (saved.state === 'live' || saved.state === 'tested' ? 4 : 3) : saved.gateway_status === 'active' ? 2 : 1);
    }).catch(e => { if (active) setResumeError(`Could not resume saved commissioning: ${String(e)}. No new claim was created.`); })
      .finally(() => { if (active) setResuming(false); });
    return () => { active = false; };
  }, [resumeId]);
  useEffect(() => { api<Site[]>('/v1/sites').then(s => { setSites(s); if (s[0] && !resumeId) setSite(s[0].id); }).catch(e => setMsg(String(e))); }, []);
  useEffect(() => { api<Profile[]>('/v1/profiles').then(setProfiles).catch(() => {}); }, []);
  useEffect(() => {
    if (session?.qr_payload && qrRef.current) QRCode.toCanvas(qrRef.current, session.qr_payload, { width: 200 }).catch(() => {});
  }, [session?.qr_payload]);

  // Poll the session while waiting on the gateway or a probe result.
  useEffect(() => {
    if (!session) return;
    const wants = (step === 1 && session.gateway_status !== 'active') || step === 3;
    if (!wants) return;
    const t = setInterval(async () => {
      try {
        const s = await api<Session>(`/v1/commissioning/sessions/${session.session_id}`);
        setSession(prev => ({ ...prev, ...s }));
        if (step === 1 && s.gateway_status === 'active') setStep(2);
      } catch { /* transient */ }
    }, 3000);
    return () => clearInterval(t);
  }, [session?.session_id, session?.gateway_status, step]);

  // Live preview polling on the final step.
  useEffect(() => {
    if (!session || step !== 4) return;
    let active=true;let request=0;
    const load = async () => {
      const seq=++request;
      try {
        const r = await api<{ points: PreviewPoint[];live:boolean }>(`/v1/commissioning/sessions/${session.session_id}/preview`);
        if(active&&seq===request){setPreview(r.points);setPreviewLive(r.live===true);setPreviewError('')}
      } catch {if(active&&seq===request){setPreviewLive(false);setPreviewError("Could not refresh source readings. Any values below are from the last successful check.")} }
    };
    load();
    const t = setInterval(load, 5000);
    return () => {active=false;clearInterval(t)};
  }, [session?.session_id, step]);

  async function start(e: React.FormEvent) {
    e.preventDefault(); setMsg('');
    if (creationAttempted.current) return;
    if (!site) { setMsg('Create a site first (use Create site above), then continue.'); return; }
    creationAttempted.current = true; setCreating(true);
    try {
      const r = await api<Session & { claim_code: string; qr_payload: string }>('/v1/commissioning/sessions', {
        method: 'POST', body: JSON.stringify({ site_id: site, serial })
      });
      const url = new URL(window.location.href); url.searchParams.set('session', r.session_id);
      window.history.replaceState(null, '', url);
      setSession(r); setStep(1);
    } catch { setCreationUncertain(true); setMsg('Could not confirm whether the gateway setup was created. Reload saved setups before trying to create anything else. No automatic retry was sent.'); }
    finally { setCreating(false); }
  }

  function connectionPayload() {
    if (!driver) return undefined;
    const base = { interval_seconds: +conn.interval_seconds };
    if (driver === 'opcua') return { ...base, endpoint: conn.endpoint };
    if (EXTRA_DRIVERS[driver]) {
      if (driver === 'can') return { ...base, port: conn.port };
      const x: Record<string, unknown> = { ...base, host: conn.host };
      if (+conn.net_port > 0) x.net_port = +conn.net_port;
      if (driver === 'iec104' || driver === 'dnp3') x.address = +conn.address;
      if (driver === 'snmp') {
        x.snmp_version = conn.snmp_version || '2c';
        if (conn.community_env) x.community_env = conn.community_env;
        if (x.snmp_version === '3') { x.snmp_auth = conn.snmp_auth || 'sha256'; x.snmp_auth_pass_env = conn.snmp_auth_pass_env; if (conn.snmp_priv_pass_env) { x.snmp_priv = conn.snmp_priv || 'aes'; x.snmp_priv_pass_env = conn.snmp_priv_pass_env; } }
      }
      return x;
    }
    if (driver === 'modbus-tcp') return { ...base, host: conn.host, net_port: +conn.net_port || 502, address: +conn.address };
    return { ...base, port: conn.port, baud: +conn.baud, data_bits:+conn.data_bits, stop_bits:+conn.stop_bits, parity:conn.parity, address: +conn.address };
  }

  async function assignProfile(e: React.FormEvent) {
    e.preventDefault(); setMsg('');
    try {
      const created=await api<{device_id:string}>(`/v1/commissioning/sessions/${session!.session_id}/profile`, {
        method: 'POST', body: JSON.stringify({ profile_id: profileId, device_name: deviceName, connection: connectionPayload() })
      });
      if (!isNet) {const first=profiles.find(p=>p.id===profileId)?.points?.[0];setProbe(pr => ({ ...pr, port: conn.port, baud: +conn.baud, data_bits:+conn.data_bits, stop_bits:+conn.stop_bits, parity:conn.parity, address: +conn.address,...(first?{register:first.register,func:first.func??4,type:first.type??'u16',word_order:first.word_order??'abcd',count:['f32','u32','i32'].includes(first.type??'')?2:1}:{}) }));}
      setSession(prev=>prev?({...prev,device_id:created.device_id,profile_id:profileId,source:{device_id:created.device_id,name:deviceName,driver,connection:connectionPayload()??{}},state:'profiled'}):prev);
      setStep(3);
    } catch (e2) { setMsg(String(e2)); }
  }

  async function runProbe(e: React.FormEvent) {
    e.preventDefault(); setMsg('');
    if (resumeId && !resumeProbeReviewed) { setMsg('Review and confirm the probe settings before sending a test.'); return; }
    try {
      await api(`/v1/commissioning/sessions/${session!.session_id}/port-test`, {
        method: 'POST', body: JSON.stringify({ ...probe, baud: +probe.baud, data_bits: +probe.data_bits, stop_bits: +probe.stop_bits, address: +probe.address, func: +probe.func, register: +probe.register, count: +probe.count, timeout_ms: +probe.timeout_ms })
      });
      setMsg('Probe sent to gateway; waiting for the result...');
    } catch (e2) { setMsg(String(e2)); }
  }

  const result = session?.port_test?.result;

  return (
    <>
      <h1>Gateway and data source setup</h1>
      {!resumeId && <><SetupRecovery /><details><summary>Existing identity needs secure pairing</summary><SecurePairing /></details></>}
      {resumeId ? <details style={{marginBottom:16}}><summary>Installer and connection help</summary><EdgeDownloads /></details> : <EdgeDownloads />}
      <ol className="steps">{STEPS.map((s, i) => <li key={s} className={i === step ? 'current' : i < step ? 'done' : ''}>{s}</li>)}</ol>
      <p className="muted" style={{ marginTop: -8, maxWidth: 640 }}>Step {step + 1} of {STEPS.length}. {HINTS[step]}</p>

      {resuming && <p role='status'>Loading saved commissioning session...</p>}
      {resumeError && <p role='alert'>{resumeError}</p>}
      {session && <p>Gateway {session.serial}. Your progress is saved. Readings will appear after the source is connected.</p>}
      {!resuming && !resumeError && step === 0 && (
        <form onSubmit={start}>
          <SiteSelect sites={sites} site={site} setSite={setSite} onCreated={s => setSites(prev => [...prev, s])} />
          <label>Gateway serial</label>
          <input value={serial} onChange={e => setSerial(e.target.value)} placeholder="AXON-0007" required />
          <div style={{ marginTop: 16 }}><button type="submit" disabled={creating || creationUncertain}>{creating ? 'Creating setup...' : 'Create claim code'}</button></div>
          {creationUncertain && <p><a href="/onboarding">Reload saved setups</a></p>}
        </form>
      )}

      {step === 1 && session && (
        <div>
          <h2>Claim the gateway</h2>
          <p>Show this to the installer once. It expires in 72 hours and is stored only as a hash.</p>
          {session.claim_code ? <p><strong>Claim code:</strong> <code>{session.claim_code}</code></p> : <p>The original code is not stored here. Use the original private code if still valid. Do not create another gateway or reclaim an existing identity.</p>}
          {session.enroll_string && <p>Or on the gateway run <code>edge-agent -enroll '{session.enroll_string}'</code> (one value: server address, code and serial). It contains the one-time code, so treat it as a secret.</p>}
          {session.claim_code && (
            <details open>
              <summary>Install the edge app on the gateway</summary>
              <p className="muted">Download the installer above. These archive commands are advanced alternatives. The server address below is this page's address, not proof it is reachable from the edge PC. Check Server addresses before enrollment.</p>
              <p><b>Ubuntu / Linux</b> (as root, in the unpacked folder):</p>
              <pre style={{ overflowX: 'auto' }}>{`sudo ./install.sh --server ${window.location.origin} --code ${session.claim_code} --serial ${session.serial} --yes`}</pre>
              <p><b>Windows</b> (elevated PowerShell, in the unpacked folder):</p>
              <pre style={{ overflowX: 'auto' }}>{`.\\install.ps1 -Server ${window.location.origin} -Code ${session.claim_code} -Serial ${session.serial} -Yes`}</pre>
            </details>
          )}
          <canvas ref={qrRef} />
          <p className="muted">Waiting for gateway {session.serial} to claim... this advances automatically.</p>
        </div>
      )}

      {step === 2 && (
        <form onSubmit={assignProfile}>
          <SchneiderMeterTemplate onSaved={p=>{setProfiles(prev=>[p,...prev]);setProfileId(p.id);const d=p.connection_defaults;setConn(c=>({...c,baud:String(d.baud),data_bits:String(d.data_bits),stop_bits:String(d.stop_bits),parity:String(d.parity),port:'',address:''}));}}/>
          <h2>Assign a device profile</h2>
          <label>Profile</label>
          <select value={profileId} onChange={e => {setProfileId(e.target.value);const p=profiles.find(x=>x.id===e.target.value);if(p?.connection_defaults){const d=p.connection_defaults;setConn(c=>({...c,baud:String(d.baud??c.baud),data_bits:String(d.data_bits??c.data_bits),stop_bits:String(d.stop_bits??c.stop_bits),parity:String(d.parity??c.parity),port:'',address:''}));}}} required>
            <option value="">Choose...</option>
            {profiles.map(p => <option key={p.id} value={p.id}>{p.name} ({p.driver_profile})</option>)}
          </select>
          <label>Device name</label>
          <input value={deviceName} onChange={e => setDeviceName(e.target.value)} placeholder="Main energy meter" required />
          {driver && (
            <>
              <h3 style={{ marginTop: 20 }}>Connection</h3>
              {driver === 'opcua' && (<><label>OPC UA endpoint</label><input value={conn.endpoint} onChange={e => setConn({ ...conn, endpoint: e.target.value })} placeholder="opc.tcp://192.168.1.60:4840" required /></>)}
              {driver === 'can' && (<>
                <label>CAN interface on the gateway</label><input value={conn.port} onChange={e => setConn({ ...conn, port: e.target.value })} placeholder="can0" pattern="[a-z]{1,8}[0-9]{1,3}" required />
                <p className="muted">Linux SocketCAN, receive only. The gateway never sends a frame. Each point is a signal: id:start bit:length:le|be:u|s, for example 0x123:0:16:le:u.</p>
              </>)}
              {EXTRA_DRIVERS[driver] && driver !== 'can' && (<>
                <label>Host / IP</label><input value={conn.host} onChange={e => setConn({ ...conn, host: e.target.value })} placeholder="192.168.1.50" required />
                <label>Port (blank = protocol default)</label><input type="number" value={conn.net_port || ''} onChange={e => setConn({ ...conn, net_port: +e.target.value })} />
                {(driver === 'iec104' || driver === 'dnp3') && (<><label>{driver === 'iec104' ? 'Common address (ASDU)' : 'Outstation address'}</label><input type="number" min={0} value={conn.address} onChange={e => setConn({ ...conn, address: +e.target.value })} /></>)}
                {driver === 'snmp' && (<>
                  <label>SNMP version</label>
                  <select value={conn.snmp_version || '2c'} onChange={e => setConn({ ...conn, snmp_version: e.target.value })}><option value="2c">v2c</option><option value="3">v3</option></select>
                  <label>Community env var name on the gateway (v2c)</label><input value={conn.community_env || ''} onChange={e => setConn({ ...conn, community_env: e.target.value })} placeholder="SNMP_COMMUNITY" />
                  {conn.snmp_version === '3' && (<>
                    <label>Auth password env var name</label><input value={conn.snmp_auth_pass_env || ''} onChange={e => setConn({ ...conn, snmp_auth_pass_env: e.target.value })} placeholder="SNMP_AUTH_PASS" required />
                    <label>Privacy password env var name (optional)</label><input value={conn.snmp_priv_pass_env || ''} onChange={e => setConn({ ...conn, snmp_priv_pass_env: e.target.value })} placeholder="SNMP_PRIV_PASS" />
                  </>)}
                  <p className="muted">Secrets are set as environment variables on the gateway. Only their names are stored here.</p>
                </>)}
              </>)}
              {driver === 'modbus-tcp' && (<>
                <label>Host / IP</label><input value={conn.host} onChange={e => setConn({ ...conn, host: e.target.value })} placeholder="192.168.1.50" required />
                <label>TCP port</label><input type="number" value={conn.net_port || 502} onChange={e => setConn({ ...conn, net_port: +e.target.value })} />
                <label>Unit / slave id</label><input type="number" min={0} max={255} value={conn.address} onChange={e => setConn({ ...conn, address: +e.target.value })} />
              </>)}
              {!isNet && (<>
                <SerialPortPicker gateway={session!.gateway_id} value={conn.port??''} onChange={port=>setConn(c=>({...c,port}))}/><label>Serial port (manual override)</label><input value={conn.port} onChange={e => setConn({ ...conn, port: e.target.value })} required />
                <label>Baud</label><input type="number" value={conn.baud} onChange={e => setConn({ ...conn, baud: +e.target.value })} />
                <label>Data bits</label><select value={conn.data_bits} onChange={e=>setConn({...conn,data_bits:+e.target.value})}>{[5,6,7,8].map(n=><option key={n}>{n}</option>)}</select>
                <label>Parity</label><select value={conn.parity} onChange={e=>setConn({...conn,parity:e.target.value})}><option value="none">None</option><option value="even">Even</option><option value="odd">Odd</option></select>
                <label>Stop bits</label><select value={conn.stop_bits} onChange={e=>setConn({...conn,stop_bits:+e.target.value})}><option>1</option><option>2</option></select>
                {driver !== 'serial-json' && (<><label>Modbus address</label><input type="number" min={1} max={247} value={conn.address} onChange={e => setConn({ ...conn, address: +e.target.value })} /></>)}
              </>)}
              <label>Poll interval (seconds)</label><input type="number" min={1} value={conn.interval_seconds} onChange={e => setConn({ ...conn, interval_seconds: +e.target.value })} />
            </>
          )}
          <div style={{ marginTop: 16 }}><button type="submit">Assign profile</button></div>
        </form>
      )}

      {session?.source && <section className="card" aria-label="Saved source" style={{marginBottom:16}}><h2>{session.source.name}</h2><p>Saved {session.source.driver} connection. These settings are stored, but that does not prove they are applied on the gateway.</p><dl>{Object.entries(session.source.connection).filter(([k])=>!["community_env","snmp_auth_pass_env","snmp_priv_pass_env"].includes(k)).map(([k,v])=><div key={k}><dt>{k.replaceAll('_',' ')}</dt><dd>{String(v)}</dd></div>)}</dl></section>}
      {session && step >= 3 && <SourceApply gateway={session.gateway_id} />}
      {session && step >= 3 && (
        <div className="card" style={{ margin: '16px 0' }}>
          <details><summary>Advanced: import configuration manually</summary><h3>Manual configuration import</h3>
          <p>Download this full gateway YAML, review the broker address/TLS and physical sensor settings, then import it in the installed edge manager. Choose boot startup if wanted and Start the service. Return here for the read-only port test and first live reading.</p>
          <button type="button" onClick={() => download(`/v1/gateways/${session.gateway_id}/edge-config`, 'edge-agent.yaml').catch(e => setMsg(String(e)))}>Download edge-agent.yaml</button>
          <p className="muted">This download does not start the agent or change the server network. Re-download after adding devices. A loopback broker address is not a remote connection.</p></details>
        </div>
      )}

      {step === 3 && (
        <div>
          <h2>Port test (read-only)</h2>
          {resumeId && <div className="card"><p>{session?.port_test?.config ? 'Your last requested probe settings are restored. They are not proof of a successful reading.' : 'No saved probe request was found. These are example test settings, not your saved gateway configuration.'} Confirm the port, serial settings, slave address, register and word order against your working device before sending a test. Loading this page sends no probe.</p><label><input type="checkbox" style={{width:"auto",marginRight:8,boxShadow:"none"}} checked={resumeProbeReviewed} onChange={e => setResumeProbeReviewed(e.target.checked)} /> I reviewed the probe settings for this device.</label></div>}
          {(isNet || driver === 'serial-json') && (
            <div className="card" style={{ marginBottom: 16 }}>
              <p className="muted">The read-only port test covers Modbus RTU registers. For {driver || 'this'} devices, skip to the live preview: the gateway starts polling as soon as it has the config below.</p>
              <button onClick={() => setStep(4)}>Skip to live preview</button>
            </div>
          )}
          <form onSubmit={runProbe} className="grid2">
            <label>Port</label><input value={probe.port} onChange={e => setProbe({ ...probe, port: e.target.value })} />
            <label>Baud</label><input type="number" value={probe.baud} onChange={e => setProbe({ ...probe, baud: +e.target.value })} />
            <label>Data bits</label><input type="number" value={probe.data_bits} onChange={e => setProbe({ ...probe, data_bits: +e.target.value })} />
            <label>Stop bits</label><input type="number" value={probe.stop_bits} onChange={e => setProbe({ ...probe, stop_bits: +e.target.value })} />
            <label>Parity</label>
            <select value={probe.parity} onChange={e => setProbe({ ...probe, parity: e.target.value })}>
              <option>none</option><option>odd</option><option>even</option>
            </select>
            <label>Modbus address</label><input type="number" min={1} max={247} value={probe.address} onChange={e => setProbe({ ...probe, address: +e.target.value })} />
            <label>Function</label>
            <select value={probe.func} onChange={e => setProbe({ ...probe, func: +e.target.value })}>
              <option value={1}>1 coils</option><option value={2}>2 discrete</option>
              <option value={3}>3 holding</option><option value={4}>4 input</option>
            </select>
            <label>Register</label><input type="number" min={0} max={65535} value={probe.register} onChange={e => setProbe({ ...probe, register: +e.target.value })} />
            <label>Count</label><input type="number" min={1} max={32} value={probe.count} onChange={e => setProbe({ ...probe, count: +e.target.value })} />
            <label>Type</label>
            <select value={probe.type} onChange={e => setProbe({ ...probe, type: e.target.value })}>
              <option>u16</option><option>i16</option><option>u32</option><option>i32</option><option>f32</option><option>bool</option>
            </select>
            <label>Word order</label>
            <select value={probe.word_order} onChange={e => setProbe({ ...probe, word_order: e.target.value })}>
              <option>abcd</option><option>badc</option><option>cdab</option><option>dcba</option>
            </select>
            <div style={{ marginTop: 16 }}><button type="submit" disabled={!!resumeId && !resumeProbeReviewed}>Send probe</button></div>
          </form>
          {result && (
            <div className={result.ok ? 'ok-box' : 'err-box'}>
              {result.ok
                ? <><strong>Port test passed</strong> ({result.latency_ms}ms). Readings: {result.readings?.map(r => `${r.point_id}=${r.value}`).join(', ') || 'none'}
                    <div style={{ marginTop: 12 }}><button onClick={() => setStep(4)}>Continue to live preview</button></div></>
                : <><strong>Port test failed:</strong> {result.error}. Check wiring, baud, parity and the Modbus address, then retry.</>}
            </div>
          )}
        </div>
      )}

      {step === 4 && (
        <div>
          <h2>Live preview</h2><p className="muted">Recent means a measured reading from the last 5 minutes. Compare the value and unit with your meter before using it.</p>{previewError&&<p role="alert">{previewError}</p>}

          {preview.length === 0
            ? <p className="muted">No readings yet. The gateway publishes on its poll interval; this refreshes every 5s.</p>
            : <table><thead><tr><th>Point</th><th>Value</th><th>Unit</th><th>Quality</th><th>Observed</th></tr></thead>
                <tbody>{preview.map(p => <tr key={p.point_id}><td>{p.point_id}</td><td>{p.value}</td><td>{p.unit}</td><td>{p.quality}{!p.recent_measured?' (not a recent measured reading)':''}</td><td>{new Date(p.observed_at).toLocaleTimeString()}</td></tr>)}</tbody></table>}
          {previewLive && !previewError ? <p><strong>Recent measured data is arriving.</strong> Check that the value and unit match your source. This does not certify the meter calibration or wiring.</p> : preview.length>0&&<p>No recent measured reading is confirmed. Historical, estimated or stale values do not complete source setup.</p>}
        </div>
      )}

      {msg && <p className="muted">{msg}</p>}
    </>
  );
}
