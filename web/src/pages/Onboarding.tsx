import { useEffect, useRef, useState } from 'react';
import QRCode from 'qrcode';
import { api, download, EXTRA_DRIVERS, NETWORK_DRIVERS } from '../lib/api';

// Guided commissioning wizard: site+serial -> QR claim -> profile ->
// read-only port test -> live preview. Each step polls the session, whose
// state the API derives from live evidence.

interface Session {
  session_id: string;
  gateway_id: string;
  serial: string;
  gateway_status: string;
  device_id: string | null;
  state: string;
  claim_code?: string;
  enroll_string?: string;
  qr_payload?: string;
  port_test?: { result?: { ok: boolean; error?: string; readings?: { point_id: string; value: number }[]; latency_ms?: number } } | null;
}
interface Site { id: string; name: string; }
interface Profile { id: string; name: string; driver_profile: string; }
interface PreviewPoint { point_id: string; value: number; unit: string; quality: string; observed_at: string; }

const STEPS = ['Site & claim code', 'Gateway claim', 'Device profile', 'Port test', 'Live preview'];
const HINTS = [
  'Pick the site and name the gateway. You get a one-time claim code to enter on the gateway.',
  'Waiting for the gateway to claim itself with the code. It generates its own key and gets a certificate.',
  'Choose which sensor model and points this gateway reads. Profiles generate the edge config.',
  'Test the port from the gateway before going live. Nothing is written to the device.',
  'Confirm real readings arrive. Commissioning finishes when the first value lands.',
];

export default function Onboarding() {
  const [step, setStep] = useState(0);
  const [sites, setSites] = useState<Site[]>([]);
  const [site, setSite] = useState('');
  const [serial, setSerial] = useState('');
  const [session, setSession] = useState<Session | null>(null);
  const [profiles, setProfiles] = useState<Profile[]>([]);
  const [profileId, setProfileId] = useState('');
  const [deviceName, setDeviceName] = useState('');
  const [probe, setProbe] = useState({ port: '/dev/ttyUSB0', baud: 9600, data_bits: 8, stop_bits: 1, parity: 'none', address: 1, func: 3, register: 0, count: 2, type: 'f32', word_order: 'abcd', timeout_ms: 3000 });
  const [conn, setConn] = useState<Record<string, any>>({ port: '/dev/ttyUSB0', baud: 9600, address: 1, host: '', net_port: 0, endpoint: 'opc.tcp://', interval_seconds: 10 }); // eslint-disable-line @typescript-eslint/no-explicit-any
  const [preview, setPreview] = useState<PreviewPoint[]>([]);
  const [msg, setMsg] = useState('');
  const driver = profiles.find(p => p.id === profileId)?.driver_profile ?? '';
  const isNet = NETWORK_DRIVERS.includes(driver);
  const qrRef = useRef<HTMLCanvasElement>(null);

  useEffect(() => { api<Site[]>('/v1/sites').then(s => { setSites(s); if (s[0]) setSite(s[0].id); }).catch(e => setMsg(String(e))); }, []);
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
    const load = async () => {
      try {
        const r = await api<{ points: PreviewPoint[] }>(`/v1/commissioning/sessions/${session.session_id}/preview`);
        setPreview(r.points);
      } catch { /* no device yet */ }
    };
    load();
    const t = setInterval(load, 5000);
    return () => clearInterval(t);
  }, [session?.session_id, step]);

  async function start(e: React.FormEvent) {
    e.preventDefault(); setMsg('');
    try {
      const r = await api<Session & { claim_code: string; qr_payload: string }>('/v1/commissioning/sessions', {
        method: 'POST', body: JSON.stringify({ site_id: site, serial })
      });
      setSession(r); setStep(1);
    } catch (e2) { setMsg(String(e2)); }
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
    return { ...base, port: conn.port, baud: +conn.baud, address: +conn.address };
  }

  async function assignProfile(e: React.FormEvent) {
    e.preventDefault(); setMsg('');
    try {
      await api(`/v1/commissioning/sessions/${session!.session_id}/profile`, {
        method: 'POST', body: JSON.stringify({ profile_id: profileId, device_name: deviceName, connection: connectionPayload() })
      });
      if (!isNet) setProbe(pr => ({ ...pr, port: conn.port, baud: +conn.baud, address: +conn.address }));
      setStep(3);
    } catch (e2) { setMsg(String(e2)); }
  }

  async function runProbe(e: React.FormEvent) {
    e.preventDefault(); setMsg('');
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
      <h1>Commission a sensor</h1>
      <ol className="steps">{STEPS.map((s, i) => <li key={s} className={i === step ? 'current' : i < step ? 'done' : ''}>{s}</li>)}</ol>
      <p className="muted" style={{ marginTop: -8, maxWidth: 640 }}>Step {step + 1} of {STEPS.length}. {HINTS[step]}</p>

      {step === 0 && (
        <form onSubmit={start}>
          <label>Site</label>
          <select value={site} onChange={e => setSite(e.target.value)} required>
            {sites.map(s => <option key={s.id} value={s.id}>{s.name}</option>)}
          </select>
          <label>Gateway serial</label>
          <input value={serial} onChange={e => setSerial(e.target.value)} placeholder="AXON-0007" required />
          <div style={{ marginTop: 16 }}><button type="submit">Create claim code</button></div>
        </form>
      )}

      {step === 1 && session && (
        <div>
          <h2>Claim the gateway</h2>
          <p>Show this to the installer once. It expires in 72 hours and is stored only as a hash.</p>
          <p><strong>Claim code:</strong> <code>{session.claim_code}</code></p>
          {session.enroll_string && <p>Or on the gateway run <code>edge-agent -enroll '{session.enroll_string}'</code> (one value: server address, code and serial). It contains the one-time code, so treat it as a secret.</p>}
          <canvas ref={qrRef} />
          <p className="muted">Waiting for gateway {session.serial} to claim... this advances automatically.</p>
        </div>
      )}

      {step === 2 && (
        <form onSubmit={assignProfile}>
          <h2>Assign a device profile</h2>
          <label>Profile</label>
          <select value={profileId} onChange={e => setProfileId(e.target.value)} required>
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
                <label>Serial port (COM3 on Windows, /dev/ttyACM0 on Linux)</label><input value={conn.port} onChange={e => setConn({ ...conn, port: e.target.value })} required />
                <label>Baud</label><input type="number" value={conn.baud} onChange={e => setConn({ ...conn, baud: +e.target.value })} />
                {driver !== 'serial-json' && (<><label>Modbus address</label><input type="number" min={0} max={255} value={conn.address} onChange={e => setConn({ ...conn, address: +e.target.value })} /></>)}
              </>)}
              <label>Poll interval (seconds)</label><input type="number" min={1} value={conn.interval_seconds} onChange={e => setConn({ ...conn, interval_seconds: +e.target.value })} />
            </>
          )}
          <div style={{ marginTop: 16 }}><button type="submit">Assign profile</button></div>
        </form>
      )}

      {step === 3 && (
        <div>
          <h2>Port test (read-only)</h2>
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
            <div style={{ marginTop: 16 }}><button type="submit">Send probe</button></div>
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
          <h2>Live preview</h2>
          <div className="card" style={{ marginBottom: 16 }}>
            <b>Gateway config</b>
            <p className="muted">Download the generated agent config and install it on the gateway (Ubuntu or Windows). Re-download after adding devices, or push it as a fleet config release.</p>
            <button className="ghost" onClick={() => download(`/v1/gateways/${session!.gateway_id}/edge-config`, 'edge-agent.yaml').catch(e => setMsg(String(e)))}>Download edge-agent.yaml</button>
          </div>
          {preview.length === 0
            ? <p className="muted">No readings yet. The gateway publishes on its poll interval; this refreshes every 5s.</p>
            : <table><thead><tr><th>Point</th><th>Value</th><th>Unit</th><th>Quality</th><th>Observed</th></tr></thead>
                <tbody>{preview.map(p => <tr key={p.point_id}><td>{p.point_id}</td><td>{p.value}</td><td>{p.unit}</td><td>{p.quality}</td><td>{new Date(p.observed_at).toLocaleTimeString()}</td></tr>)}</tbody></table>}
          {preview.length > 0 && <p><strong>Commissioning complete.</strong> The sensor is live end to end.</p>}
        </div>
      )}

      {msg && <p className="muted">{msg}</p>}
    </>
  );
}
