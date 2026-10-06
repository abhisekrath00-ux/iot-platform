import { useCallback, useEffect, useState } from 'react';
import { api } from '../lib/api';

interface EP { id: string; name: string; url: string; site_id: string; priority: number; loopback: boolean; }
interface Data { endpoints: EP[]; resolved: string[]; resolved_source: string; suggestions: string[]; warnings: string[]; }
interface Site { id: string; name: string; }
interface Check { ok: boolean; error?: string; ms?: number; scope?: string; }

export default function ServerAddresses() {
  const [d, setD] = useState<Data | null>(null);
  const [sites, setSites] = useState<Site[]>([]);
  const [name, setName] = useState('');
  const [url, setUrl] = useState('');
  const [site, setSite] = useState('');
  const [prio, setPrio] = useState(100);
  const [msg, setMsg] = useState('');
  const [chk, setChk] = useState<Record<string, Check>>({});
  const load = useCallback(() => { api<Data>('/v1/system/endpoints').then((x) => setD({ ...x, endpoints: x.endpoints ?? [], resolved: x.resolved ?? [], suggestions: x.suggestions ?? [], warnings: x.warnings ?? [] })).catch((e) => setMsg(String(e.message ?? e))); }, []);
  useEffect(() => { load(); api<Site[]>('/v1/sites').then(setSites).catch(() => {}); }, [load]);
  const check = (u: string) => api<Check>('/v1/system/endpoints/check', { method: 'POST', body: JSON.stringify({ url: u }) }).then((r) => setChk((c) => ({ ...c, [u]: r }))).catch((e) => setMsg(String(e.message ?? e)));
  const add = async () => {
    setMsg('');
    try { await api('/v1/system/endpoints', { method: 'POST', body: JSON.stringify({ name, url, site_id: site, priority: prio }) }); setName(''); setUrl(''); load(); }
    catch (e) { setMsg(String((e as Error).message ?? e)); }
  };
  const del = (id: string) => api(`/v1/system/endpoints/${id}`, { method: 'DELETE' }).then(load).catch((e) => setMsg(String(e.message ?? e)));
  if (!d) return <div className="card"><b>Server addresses</b><p className="muted">{msg || 'Loading…'}</p></div>;
  return (
    <div className="card">
      <b>Server addresses for edge devices</b>
      <p className="muted">The address edge boxes use to reach this platform. Lowest priority number is tried first, the rest are fallbacks. A site address overrides the workspace one for that site (different networks, regions, NAT or a load balancer in front). Install commands never use the browser address.</p>
      {d.warnings.map((w) => <p key={w} role="alert" style={{ color: '#dc2626' }}>{w}</p>)}
      {d.resolved.length > 0 && <p>Currently handed to new installs: <code>{d.resolved.join('  then  ')}</code> ({d.resolved_source})</p>}
      <table>
        <thead><tr><th>Name</th><th>Address</th><th>Applies to</th><th>Priority</th><th /></tr></thead>
        <tbody>
          {d.endpoints.map((e) => (
            <tr key={e.id}>
              <td>{e.name}</td>
              <td><code>{e.url}</code>{e.loopback && <span style={{ color: '#dc2626' }}> loopback</span>}{chk[e.url] && <div className="muted">{chk[e.url].ok ? `answers (${chk[e.url].ms} ms)` : `no: ${chk[e.url].error}`}</div>}</td>
              <td>{e.site_id ? (sites.find((s) => s.id === e.site_id)?.name ?? e.site_id) : 'All sites'}</td>
              <td>{e.priority}</td>
              <td><button className="secondary" onClick={() => check(e.url)}>Test</button> <button className="secondary" onClick={() => del(e.id)}>Remove</button></td>
            </tr>
          ))}
          {!d.endpoints.length && <tr><td colSpan={5} className="muted">None configured.</td></tr>}
        </tbody>
      </table>
      <div style={{ display: 'flex', gap: 8, flexWrap: 'wrap', marginTop: 10 }}>
        <input style={{ width: 160 }} aria-label="Name" placeholder="Name (e.g. Plant LAN)" value={name} onChange={(e) => setName(e.target.value)} />
        <input style={{ width: 260 }} aria-label="Address" placeholder="https://hub.example.com:8443" value={url} onChange={(e) => setUrl(e.target.value)} list="ep-suggest" />
        <datalist id="ep-suggest">{d.suggestions.map((s) => <option key={s} value={s} />)}</datalist>
        <select style={{ width: 'auto' }} aria-label="Site" value={site} onChange={(e) => setSite(e.target.value)}><option value="">All sites</option>{sites.map((s) => <option key={s.id} value={s.id}>{s.name}</option>)}</select>
        <input style={{ width: 90 }} type="number" aria-label="Priority" value={prio} onChange={(e) => setPrio(Number(e.target.value))} />
        <button onClick={add} disabled={!name || !url}>Add address</button>
      </div>
      {d.suggestions.length > 0 && <p className="muted">Suggestions from this server's network interfaces (pick the one your edge boxes can reach): {d.suggestions.map((s) => <button key={s} className="secondary" style={{ marginRight: 6 }} onClick={() => setUrl(s)}>{s}</button>)}</p>}
      <p className="muted">Test asks the address from this server. It shows the server can reach it, not that a remote edge network can. Behind a load balancer or NAT, use its public name.</p>
      {msg && <p role="alert">{msg}</p>}
    </div>
  );
}
