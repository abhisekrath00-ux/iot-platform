import { useEffect, useState } from 'react';
import { api } from '../lib/api';

interface Gateway { id: string; serial: string; status: string; kind: string; }
interface Profile { id: string; name: string; driver_profile: string; }
interface Match { profile_id: string; name: string; score: number; probed: number; }
interface Slave { address: number; matches: Match[]; }
interface Host { Addr: string; Port: number; Service: string; }
interface Bac { Addr: string; Instance: number; Vendor: number; }
interface ScanRow {
  id: string; kind: 'modbus-rtu' | 'lan' | 'bacnet'; status: string; created_at: string;
  params: { port?: string; baud?: number; parity?: string };
  result?: { ok: boolean; error?: string; note?: string; slaves?: Slave[]; hosts?: Host[]; bacnet?: Bac[] };
}

const PORT_DRIVER: Record<number, string> = { 502: 'modbus-tcp', 4840: 'opcua', 2404: 'iec104', 20000: 'dnp3', 102: 'iec61850' };

// Scan, then one-click add. The gateway scans read-only; nothing is added until
// you pick a profile and press Add. Suggestions show how plausible a profile is
// (its first registers answered inside the profile's ranges), not proof of the model.
export default function Scan() {
  const [gws, setGws] = useState<Gateway[]>([]);
  const [gw, setGw] = useState('');
  const [profiles, setProfiles] = useState<Profile[]>([]);
  const [scans, setScans] = useState<ScanRow[]>([]);
  const [kind, setKind] = useState<ScanRow['kind']>('modbus-rtu');
  const [f, setF] = useState({ port: '/dev/ttyUSB0', baud: 9600, parity: 'none', from: 1, to: 32, cidr: '192.168.1.0/24', broadcast: '192.168.1.255' });
  const [msg, setMsg] = useState('');
  const [added, setAdded] = useState<Record<string, string>>({});

  useEffect(() => {
    api<Gateway[]>('/v1/gateways').then(g => { setGws(g); const a = g.find(x => x.status === 'active') ?? g[0]; if (a) setGw(a.id); }).catch(e => setMsg(String(e)));
    api<Profile[]>('/v1/profiles').then(setProfiles).catch(() => undefined);
  }, []);
  const load = () => gw ? api<ScanRow[]>(`/v1/gateways/${gw}/scans`).then(setScans).catch(e => setMsg(String(e))) : undefined;
  useEffect(() => { load(); }, [gw]); // eslint-disable-line react-hooks/exhaustive-deps
  const latest = scans[0];
  const running = latest?.status === 'requested';
  useEffect(() => {
    if (!running) return;
    const t = setInterval(load, 3000);
    return () => clearInterval(t);
  }, [running, gw]); // eslint-disable-line react-hooks/exhaustive-deps

  async function start(e: React.FormEvent) {
    e.preventDefault(); setMsg('');
    const params = kind === 'modbus-rtu' ? { port: f.port, baud: +f.baud, parity: f.parity, from: +f.from, to: +f.to } : kind === 'lan' ? { cidr: f.cidr } : { broadcast: f.broadcast };
    try {
      await api(`/v1/gateways/${gw}/scans`, { method: 'POST', body: JSON.stringify({ kind, params }) });
      setAdded({}); load();
    } catch (e2) { setMsg(String(e2)); }
  }

  async function add(s: ScanRow, key: string, profileId: string, name: string, connection: Record<string, unknown>) {
    setMsg('');
    try {
      await api(`/v1/gateways/${gw}/scans/${s.id}/add`, { method: 'POST', body: JSON.stringify({ profile_id: profileId, device_name: name, connection }) });
      setAdded(a => ({ ...a, [key]: name }));
    } catch (e2) { setMsg(String(e2)); }
  }

  return (
    <>
      <h1>Scan for devices</h1>
      <p className="muted">The gateway looks for devices on its own serial port or network (read-only), then you pick a profile and add. Nothing is added automatically. Needs a connected gateway; a serial scan fails if another device is already polling that port.</p>
      <div className="card" style={{ maxWidth: 760, marginBottom: 20 }}>
        <form onSubmit={start}>
          <label>Gateway</label>
          <select value={gw} onChange={e => setGw(e.target.value)}>{gws.map(g => <option key={g.id} value={g.id}>{g.serial} ({g.status})</option>)}</select>
          <label>What to scan</label>
          <select value={kind} onChange={e => setKind(e.target.value as ScanRow['kind'])}>
            <option value="modbus-rtu">Serial Modbus (RS-485 slave addresses)</option>
            <option value="lan">Network (Modbus TCP, OPC UA, IEC 104, DNP3, ports)</option>
            <option value="bacnet">BACnet/IP (Who-Is)</option>
          </select>
          {kind === 'modbus-rtu' && (<>
            <label>Serial port on the gateway</label><input value={f.port} onChange={e => setF({ ...f, port: e.target.value })} required />
            <label>Baud</label><select value={f.baud} onChange={e => setF({ ...f, baud: +e.target.value })}>{[1200, 2400, 4800, 9600, 19200, 38400, 57600, 115200].map(b => <option key={b}>{b}</option>)}</select>
            <label>Parity</label><select value={f.parity} onChange={e => setF({ ...f, parity: e.target.value })}><option>none</option><option>even</option><option>odd</option></select>
            <label>Addresses from / to (1-247; a wide range takes minutes)</label>
            <div style={{ display: 'flex', gap: 8 }}><input type="number" min={1} max={247} value={f.from} onChange={e => setF({ ...f, from: +e.target.value })} /><input type="number" min={1} max={247} value={f.to} onChange={e => setF({ ...f, to: +e.target.value })} /></div>
          </>)}
          {kind === 'lan' && (<><label>Private network range (max /22)</label><input value={f.cidr} onChange={e => setF({ ...f, cidr: e.target.value })} required /></>)}
          {kind === 'bacnet' && (<><label>Subnet broadcast address</label><input value={f.broadcast} onChange={e => setF({ ...f, broadcast: e.target.value })} required /></>)}
          <div style={{ marginTop: 14 }}><button type="submit" disabled={!gw || running}>{running ? 'Scanning...' : 'Start scan'}</button></div>
        </form>
        {msg && <p className="muted" role="alert">{msg}</p>}
      </div>

      {latest && (
        <div className="card" style={{ maxWidth: 900 }}>
          <b>Latest scan: {latest.kind}</b> <span className="muted">{latest.status === 'requested' ? 'running, waiting for the gateway...' : latest.status}</span>
          {latest.result?.error && <p role="alert">{latest.result.error}</p>}
          {latest.result?.note && <p className="muted">{latest.result.note}</p>}
          {latest.kind === 'modbus-rtu' && latest.status === 'done' && (
            <SlaveTable s={latest} profiles={profiles.filter(p => p.driver_profile.startsWith('modbus'))} added={added} add={add} />
          )}
          {latest.kind === 'lan' && latest.status === 'done' && (
            <HostTable s={latest} profiles={profiles} added={added} add={add} />
          )}
          {latest.kind === 'bacnet' && latest.status === 'done' && (
            <BacTable s={latest} profiles={profiles.filter(p => p.driver_profile === 'bacnet')} added={added} add={add} />
          )}
          {latest.status === 'timed_out' && <p>The gateway did not answer within 10 minutes. Check that it is online and running a current agent.</p>}
        </div>
      )}
    </>
  );
}

type AddFn = (s: ScanRow, key: string, profileId: string, name: string, c: Record<string, unknown>) => void;

function SlaveTable({ s, profiles, added, add }: { s: ScanRow; profiles: Profile[]; added: Record<string, string>; add: AddFn }) {
  const slaves = s.result?.slaves ?? [];
  const [sel, setSel] = useState<Record<number, string>>({});
  const [names, setNames] = useState<Record<number, string>>({});
  if (slaves.length === 0) return <p className="muted">No slave answered. Check wiring (A/B swap), baud, parity and the address range. A device that rejects the probed registers is not detected.</p>;
  return (
    <table><thead><tr><th>Address</th><th>Suggested</th><th>Profile</th><th>Name</th><th /></tr></thead><tbody>
      {slaves.map(sl => {
        const best = sl.matches[0];
        const pid = sel[sl.address] ?? best?.profile_id ?? '';
        const key = `rtu-${sl.address}`;
        return (
          <tr key={sl.address}>
            <td>{sl.address}</td>
            <td className="muted">{best ? `${best.name} (${Math.round(best.score * 100)}% of ${best.probed} registers plausible)` : 'no profile matched'}</td>
            <td><select value={pid} onChange={e => setSel({ ...sel, [sl.address]: e.target.value })}><option value="">Choose...</option>{profiles.map(p => <option key={p.id} value={p.id}>{p.name}</option>)}</select></td>
            <td><input value={names[sl.address] ?? `Slave ${sl.address}`} onChange={e => setNames({ ...names, [sl.address]: e.target.value })} aria-label={`Name for slave ${sl.address}`} /></td>
            <td>{added[key] ? <span className="muted">Added</span> : <button disabled={!pid} onClick={() => add(s, key, pid, names[sl.address] ?? `Slave ${sl.address}`, { port: s.params.port, baud: s.params.baud, parity: s.params.parity ?? 'none', address: sl.address, interval_seconds: 10 })}>Add</button>}</td>
          </tr>
        );
      })}
    </tbody></table>
  );
}

function HostTable({ s, profiles, added, add }: { s: ScanRow; profiles: Profile[]; added: Record<string, string>; add: AddFn }) {
  const hosts = s.result?.hosts ?? [];
  const [sel, setSel] = useState<Record<string, string>>({});
  const [addr, setAddr] = useState<Record<string, number>>({});
  if (hosts.length === 0) return <p className="muted">No open industrial ports found in that range.</p>;
  return (
    <>
      <p className="muted">An open port means something is listening, not that it is a supported device.</p>
      <table><thead><tr><th>Host</th><th>Port</th><th>Looks like</th><th>Profile</th><th>Unit / address</th><th /></tr></thead><tbody>
        {hosts.map(h => {
          const key = `${h.Addr}:${h.Port}`;
          const drv = PORT_DRIVER[h.Port];
          const opts = profiles.filter(p => p.driver_profile === drv);
          const pid = sel[key] ?? opts[0]?.id ?? '';
          const needsAddr = drv === 'modbus-tcp' || drv === 'iec104' || drv === 'dnp3';
          const conn = drv === 'opcua' ? { endpoint: `opc.tcp://${h.Addr}:${h.Port}`, host: h.Addr, net_port: h.Port, interval_seconds: 10 } : { host: h.Addr, net_port: h.Port, interval_seconds: 10, ...(needsAddr ? { address: addr[key] ?? 1 } : {}) };
          return (
            <tr key={key}>
              <td>{h.Addr}</td><td>{h.Port}</td><td className="muted">{h.Service}</td>
              <td>{drv ? <select value={pid} onChange={e => setSel({ ...sel, [key]: e.target.value })}><option value="">Choose...</option>{opts.map(p => <option key={p.id} value={p.id}>{p.name}</option>)}</select> : <span className="muted">not addable here</span>}</td>
              <td>{needsAddr && <input type="number" min={0} style={{ width: 70 }} value={addr[key] ?? 1} onChange={e => setAddr({ ...addr, [key]: +e.target.value })} aria-label={`Address for ${key}`} />}</td>
              <td>{added[key] ? <span className="muted">Added</span> : <button disabled={!pid} onClick={() => add(s, key, pid, `${h.Service} ${h.Addr}`, conn)}>Add</button>}</td>
            </tr>
          );
        })}
      </tbody></table>
    </>
  );
}

function BacTable({ s, profiles, added, add }: { s: ScanRow; profiles: Profile[]; added: Record<string, string>; add: AddFn }) {
  const devs = s.result?.bacnet ?? [];
  const [sel, setSel] = useState<Record<string, string>>({});
  if (devs.length === 0) return <p className="muted">No BACnet device answered. Broadcasts do not cross routers or VLANs; use the subnet's broadcast address.</p>;
  return (
    <table><thead><tr><th>Address</th><th>Device</th><th>Vendor id</th><th>Profile</th><th /></tr></thead><tbody>
      {devs.map(d => {
        const key = d.Addr;
        const pid = sel[key] ?? profiles[0]?.id ?? '';
        return (
          <tr key={key}>
            <td>{d.Addr}</td><td>{d.Instance}</td><td>{d.Vendor}</td>
            <td><select value={pid} onChange={e => setSel({ ...sel, [key]: e.target.value })}><option value="">Choose...</option>{profiles.map(p => <option key={p.id} value={p.id}>{p.name}</option>)}</select></td>
            <td>{added[key] ? <span className="muted">Added</span> : <button disabled={!pid} onClick={() => add(s, key, pid, `BACnet ${d.Instance}`, { host: d.Addr, interval_seconds: 10 })}>Add</button>}</td>
          </tr>
        );
      })}
    </tbody></table>
  );
}
