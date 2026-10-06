import { useEffect, useState } from 'react';
import { api } from '../lib/api';
import { DeviceInput, EDGE_TEMPLATES, deviceYaml, validateDevice } from '../lib/edgeSetup';

interface Site { id: string; name: string; }
interface Minted { server_url: string; fallback_urls?: string[]; warnings?: string[]; gateway_id: string; claim_code: string; expires_at: string; enroll_string: string; }
interface Gw { id: string; serial: string; status: string; kind: string; }

function save(name: string, text: string) {
  const url = URL.createObjectURL(new Blob([text], { type: 'text/plain' }));
  const a = document.createElement('a'); a.href = url; a.download = name; a.click(); URL.revokeObjectURL(url);
}

export default function EdgeSetup() {
  const [sites, setSites] = useState<Site[]>([]);
  const [site, setSite] = useState('');
  const [serial, setSerial] = useState('');
  const [dev, setDev] = useState<DeviceInput>({ deviceId: 'meter-1', templateId: EDGE_TEMPLATES[0].id, link: 'rtu', port: '/dev/ttyUSB0', baud: 9600, parity: 'none', host: '', tcpPort: 502, address: 1, intervalSec: 10 });
  const [minted, setMinted] = useState<Minted | null>(null);
  const [gw, setGw] = useState<Gw | null>(null);
  const [msg, setMsg] = useState('');
  const [busy, setBusy] = useState(false);
  const errs = validateDevice(dev);
  const tpl = EDGE_TEMPLATES.find((t) => t.id === dev.templateId)!;
  const [addr, setAddr] = useState<{ resolved: string[]; warnings: string[] } | null>(null);
  const api_url = minted?.server_url ?? addr?.resolved?.[0] ?? 'https://YOUR-SERVER-ADDRESS';
  const allAddr = minted ? [minted.server_url, ...(minted.fallback_urls ?? [])] : (addr?.resolved?.length ? addr.resolved : [api_url]);
  const warns = [...(minted?.warnings ?? addr?.warnings ?? [])];

  useEffect(() => { api<Site[]>('/v1/sites').then((s) => { setSites(s); if (s[0]) setSite(s[0].id); }).catch((e) => setMsg(String(e.message ?? e))); }, []);
  useEffect(() => { if (site) api<{ resolved: string[]; warnings: string[] }>(`/v1/system/endpoints?site=${encodeURIComponent(site)}`).then(setAddr).catch(() => setAddr(null)); }, [site]);
  useEffect(() => {
    if (!minted) return;
    const t = setInterval(() => api<Gw[]>('/v1/gateways').then((l) => setGw(l.find((g) => g.id === minted.gateway_id) ?? null)).catch(() => {}), 5000);
    return () => clearInterval(t);
  }, [minted]);

  async function mint() {
    setBusy(true); setMsg('');
    try { setMinted(await api<Minted>('/v1/enrollment/tokens', { method: 'POST', body: JSON.stringify({ site_id: site, serial: serial.trim(), ttl_hours: 72, kind: 'edge' }) })); }
    catch (e) { setMsg(String((e as Error).message ?? e)); }
    setBusy(false);
  }
  const num = (k: keyof DeviceInput) => (e: React.ChangeEvent<HTMLInputElement | HTMLSelectElement>) => setDev({ ...dev, [k]: Number(e.target.value) });
  const lin = `sudo ./install.sh --server ${allAddr.join(',')} --code ${minted?.claim_code ?? 'CODE'} --serial ${serial || 'SERIAL'} --yes`;
  const win = `.\\install.ps1 -Server ${allAddr.join(',')} -Code ${minted?.claim_code ?? 'CODE'} -Serial ${serial || 'SERIAL'}`;
  const step = (n: number, t: string, done: boolean) => <span style={{ marginRight: 16, fontWeight: 600, opacity: done ? 1 : 0.6 }}>{done ? '✔' : n}. {t}</span>;

  return (
    <div>
      <h2>Set up an edge device</h2>
      <p className="muted">Describe the device, download its settings, install the edge agent on the box next to it, plug the sensor in. The platform then shows the data.</p>
      <div style={{ marginBottom: 12 }}>{step(1, 'Describe', !!serial && !errs.length)}{step(2, 'Get claim code', !!minted)}{step(3, 'Install and connect', gw?.status === 'active' || gw?.status === 'online')}</div>

      <div className="card">
        <h3>1. Describe the edge device and sensor</h3>
        <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fill,minmax(220px,1fr))', gap: 10 }}>
          <label>Site<select value={site} onChange={(e) => setSite(e.target.value)}>{sites.map((s) => <option key={s.id} value={s.id}>{s.name}</option>)}</select></label>
          <label>Edge box serial or name<input value={serial} onChange={(e) => setSerial(e.target.value)} placeholder="e.g. HX-PLANT1-01" /></label>
          <label>Sensor template<select value={dev.templateId} onChange={(e) => setDev({ ...dev, templateId: e.target.value })}>{EDGE_TEMPLATES.map((t) => <option key={t.id} value={t.id}>{t.name}</option>)}</select></label>
          <label>Device id<input value={dev.deviceId} onChange={(e) => setDev({ ...dev, deviceId: e.target.value })} /></label>
          <label>Connection<select value={dev.link} onChange={(e) => setDev({ ...dev, link: e.target.value as 'rtu' | 'tcp' })}><option value="rtu">RS-485 serial (Modbus RTU)</option><option value="tcp">Network (Modbus TCP)</option></select></label>
          {dev.link === 'rtu' ? <>
            <label>Serial port<input value={dev.port} onChange={(e) => setDev({ ...dev, port: e.target.value })} /></label>
            <label>Baud<select value={dev.baud} onChange={num('baud')}>{[1200, 2400, 4800, 9600, 19200, 38400, 57600, 115200].map((b) => <option key={b}>{b}</option>)}</select></label>
            <label>Parity<select value={dev.parity} onChange={(e) => setDev({ ...dev, parity: e.target.value as 'none' })}><option>none</option><option>even</option><option>odd</option></select></label>
          </> : <>
            <label>Host or IP<input value={dev.host} onChange={(e) => setDev({ ...dev, host: e.target.value })} /></label>
            <label>TCP port<input type="number" value={dev.tcpPort} onChange={num('tcpPort')} /></label>
          </>}
          <label>Modbus address<input type="number" value={dev.address} onChange={num('address')} /></label>
          <label>Read every (s)<input type="number" value={dev.intervalSec} onChange={num('intervalSec')} /></label>
        </div>
        <p className="muted">{tpl.note}</p>
        <table><thead><tr><th>Point</th><th>Register</th><th>Unit</th><th>Valid range</th></tr></thead><tbody>
          {tpl.points.map((x) => <tr key={x.id}><td>{x.id}</td><td>{x.register}</td><td>{x.unit || '-'}</td><td>{x.min} to {x.max}</td></tr>)}
        </tbody></table>
        {errs.map((e) => <p key={e} role="alert" style={{ color: '#dc2626' }}>{e}</p>)}
      </div>

      <div className="card" style={{ marginTop: 12 }}>
        <h3>2. Get the claim code and settings</h3>
        <button disabled={busy || !site || !serial.trim() || errs.length > 0 || !!minted} onClick={mint}>Create claim code</button>{' '}
        <button className="secondary" disabled={errs.length > 0} onClick={() => save('edge-devices.yaml', deviceYaml(dev))}>Download device settings (YAML)</button>
        {msg && <p role="alert">{msg}</p>}
        {minted && <p role="status">Claim code <code>{minted.claim_code}</code>, valid until {new Date(minted.expires_at).toLocaleString()}. It is shown once. Treat it like a password.</p>}
      </div>

      <div className="card" style={{ marginTop: 12 }}>
        <h3>3. Install on the edge box, then plug in the sensor</h3>
        {warns.map((w) => <p key={w} role="alert" style={{ color: '#dc2626' }}>{w} Set it under Settings, Server addresses.</p>)}
        {minted?.fallback_urls?.length ? <p className="muted">Fallback addresses are included in the commands above (comma separated). The installer and the claim step try them in order if the first cannot be reached.</p> : null}
        <p>Download the edge package for the box from your release page (<code>hexmon-edge-&lt;ver&gt;-&lt;os&gt;-&lt;arch&gt;</code>) and verify <code>SHA256SUMS</code>. The agent needs only your server, no internet.</p>
        <p><b>Linux:</b></p><pre style={{ overflowX: 'auto' }}>{lin}</pre>
        <p><b>Windows (elevated PowerShell):</b></p><pre style={{ overflowX: 'auto' }}>{win}</pre>
        <p className="muted">Append the downloaded devices block to <code>/etc/hexmon/edge-agent.yaml</code>, run <code>edge-agent -check-config</code>, then start the service.</p>
        <p>{!minted ? 'Waiting for step 2.' : !gw ? 'Waiting for the edge box to check in…' : `Edge box ${gw.serial}: ${gw.status}.`}</p>
        <p className="muted">Honest status: the generated settings and install commands are unit-tested for format only. The full path (install, claim, MX300 read, data in the dashboard) has not been run on a real edge box or real meter. The platform never writes to the meter.</p>
      </div>
    </div>
  );
}
