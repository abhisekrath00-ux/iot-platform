import { useCallback, useEffect, useState } from 'react';
import { api } from '../lib/api';
import { CampaignRow, allowedActions, parseStages, progress } from '../lib/fleetUpdates';

interface Gw { id: string; serial: string; status: string; kind: string; site_id: string; }
interface Op { id: string; kind: string; status: string; requested_by: string; approved_by: string; result: { ok?: boolean; detail?: string; lines?: string[] } | null; created_at: string; }
interface Push { id: string; version: number; status: string; detail: string; requested_by: string; created_at: string; }
interface Site { id: string; name: string; }
interface Rel { id: string; version: string; artifact_sha256: string; notes: string; created_at: string; }

const tone = (s: string) => (s === 'active' || s === 'online' || s === 'done' ? '#16a34a' : s === 'running' ? '#2563eb' : s === 'paused' || s === 'pending' ? '#d97706' : '#dc2626');

export default function FleetUpdates() {
  const [gws, setGws] = useState<Gw[]>([]);
  const [sites, setSites] = useState<Site[]>([]);
  const [rels, setRels] = useState<Rel[]>([]);
  const [camps, setCamps] = useState<CampaignRow[]>([]);
  const [sel, setSel] = useState<Set<string>>(new Set());
  const [ver, setVer] = useState(''); const [sha, setSha] = useState(''); const [notes, setNotes] = useState('');
  const [rel, setRel] = useState(''); const [cname, setCname] = useState(''); const [stages, setStages] = useState('10,50,100'); const [thr, setThr] = useState(3);
  const [msg, setMsg] = useState('');
  const [opGw, setOpGw] = useState(''); const [ops, setOps] = useState<Op[]>([]); const [showLog, setShowLog] = useState('');
  const load = useCallback(() => {
    api<Gw[]>('/v1/gateways').then((x) => setGws(x ?? [])).catch((e) => setMsg(String(e.message ?? e)));
    api<Rel[]>('/v1/fleet/releases').then((x) => { setRels(x ?? []); if (x?.[0]) setRel((r) => r || x[0].id); }).catch(() => {});
    api<CampaignRow[]>('/v1/fleet/campaigns').then((x) => setCamps(x ?? [])).catch(() => {});
  }, []);
  useEffect(() => { load(); api<Site[]>('/v1/sites').then(setSites).catch(() => {}); const t = setInterval(load, 10000); return () => clearInterval(t); }, [load]);
  const loadOps = useCallback(() => { if (opGw) api<Op[]>(`/v1/gateways/${opGw}/ops`).then((x) => setOps(x ?? [])).catch(() => setOps([])); }, [opGw]);
  useEffect(() => { loadOps(); const t = setInterval(loadOps, 5000); return () => clearInterval(t); }, [loadOps]);
  const [pushes, setPushes] = useState<Push[]>([]);
  const loadPushes = useCallback(() => { if (opGw) api<Push[]>(`/v1/gateways/${opGw}/config-push`).then((x) => setPushes(x ?? [])).catch(() => setPushes([])); else setPushes([]); }, [opGw]);
  useEffect(() => { loadPushes(); const t = setInterval(loadPushes, 5000); return () => clearInterval(t); }, [loadPushes]);
  const approveOp = async (id: string) => {
    let body: string | undefined;
    const t = await api<{ required_for_approval: boolean }>('/v1/me/totp').catch(() => null);
    if (t?.required_for_approval) { const code = window.prompt('Authenticator code (6 digits)'); if (!code) return; body = JSON.stringify({ code }); }
    run(() => api(`/v1/gateway-ops/${id}/approve`, { method: 'POST', body }), 'Restart approved and sent.').then(loadOps);
  };
  const siteName = (id: string) => sites.find((s) => s.id === id)?.name ?? id;
  const run = async (fn: () => Promise<unknown>, ok: string) => { setMsg(''); try { await fn(); setMsg(ok); load(); } catch (e) { setMsg(String((e as Error).message ?? e)); } };
  const st = parseStages(stages);
  const toggle = (serial: string) => setSel((s) => { const n = new Set(s); n.has(serial) ? n.delete(serial) : n.add(serial); return n; });
  const selectSite = (id: string) => setSel(new Set(gws.filter((g) => g.site_id === id && g.kind === 'edge').map((g) => g.serial)));
  const post = (path: string, body?: unknown) => api(path, { method: 'POST', body: body ? JSON.stringify(body) : undefined });

  return (
    <div>
      <h2>Fleet and updates</h2>
      <p className="muted">Edge boxes, release versions and staged rollouts. A rollout goes to a small share first and only advances when that stage has no pending or failed boxes. Rollback moves boxes back to an earlier release.</p>
      {msg && <p role="status">{msg}</p>}

      <div className="card">
        <b>Edge boxes</b> <span className="muted">({sel.size} selected for a rollout)</span>
        <div style={{ margin: '6px 0' }}>Select a site: {sites.map((s) => <button key={s.id} className="secondary" style={{ marginRight: 6 }} onClick={() => selectSite(s.id)}>{s.name}</button>)}<button className="secondary" onClick={() => setSel(new Set())}>Clear</button></div>
        <table><thead><tr><th /><th>Serial</th><th>Site</th><th>Kind</th><th>Status</th></tr></thead><tbody>
          {gws.map((g) => <tr key={g.id}><td><input type="checkbox" style={{ width: "auto" }} aria-label={`Select ${g.serial}`} checked={sel.has(g.serial)} disabled={g.kind !== 'edge'} onChange={() => toggle(g.serial)} /></td><td>{g.serial}</td><td>{siteName(g.site_id)}</td><td>{g.kind}</td><td style={{ color: tone(g.status) }}>{g.status}</td></tr>)}
          {!gws.length && <tr><td colSpan={5} className="muted">No edge boxes yet. Use Edge setup to add one.</td></tr>}
        </tbody></table>
        <p className="muted">Not built yet: remote actions on a box (restart, collect logs) and per-box agent version and last-seen time in this list.</p>
      </div>

      <div className="card" style={{ marginTop: 12 }}>
        <b>Releases</b>
        <table><thead><tr><th>Version</th><th>Artifact SHA-256</th><th>Notes</th></tr></thead><tbody>
          {rels.map((r) => <tr key={r.id}><td>{r.version}</td><td><code>{r.artifact_sha256 ? r.artifact_sha256.slice(0, 16) + '…' : 'config only'}</code></td><td>{r.notes}</td></tr>)}
          {!rels.length && <tr><td colSpan={3} className="muted">No releases yet.</td></tr>}
        </tbody></table>
        <div style={{ display: 'flex', gap: 8, flexWrap: 'wrap', marginTop: 8 }}>
          <input style={{ width: 120 }} aria-label="Version" placeholder="1.4.0" value={ver} onChange={(e) => setVer(e.target.value)} />
          <input style={{ width: 300 }} aria-label="SHA-256" placeholder="artifact sha256 (blank = config only)" value={sha} onChange={(e) => setSha(e.target.value)} />
          <input style={{ width: 200 }} aria-label="Notes" placeholder="notes" value={notes} onChange={(e) => setNotes(e.target.value)} />
          <button disabled={!ver} onClick={() => run(() => post('/v1/fleet/releases', { version: ver, artifact_sha256: sha || undefined, notes }).then(() => { setVer(''); setSha(''); setNotes(''); }), 'Release added.')}>Add release</button>
        </div>
        <p className="muted">The artifact itself is placed on the box by your offline bundle; the box checks its digest and refuses a mismatch.</p>
      </div>

      <div className="card" style={{ marginTop: 12 }}>
        <b>New staged rollout</b>
        <div style={{ display: 'flex', gap: 8, flexWrap: 'wrap', margin: '6px 0' }}>
          <select style={{ width: 'auto' }} aria-label="Release" value={rel} onChange={(e) => setRel(e.target.value)}>{rels.map((r) => <option key={r.id} value={r.id}>{r.version}</option>)}</select>
          <input style={{ width: 200 }} aria-label="Name" placeholder="Name (e.g. 1.4.0 to plant 1)" value={cname} onChange={(e) => setCname(e.target.value)} />
          <input style={{ width: 130 }} aria-label="Stages" value={stages} onChange={(e) => setStages(e.target.value)} title="Percent of boxes per stage, last is 100" />
          <label>Halt after <input style={{ width: 60 }} type="number" min={1} value={thr} onChange={(e) => setThr(Number(e.target.value))} /> failures</label>
          <button disabled={!rel || !cname || !!st.error || sel.size === 0} onClick={() => run(() => post('/v1/fleet/campaigns', { release_id: rel, name: cname, stages: st.stages, gateway_serials: [...sel], failure_threshold: thr }).then(() => setCname('')), 'Rollout created (draft). Press Start to begin stage 1.')}>Create rollout</button>
        </div>
        {st.error && <p role="alert" style={{ color: '#dc2626' }}>{st.error}</p>}
        {sel.size === 0 && <p className="muted">Select boxes above first.</p>}
      </div>

      <div className="card" style={{ marginTop: 12 }}>
        <b>Remote maintenance</b>
        <p className="muted">Fetch an edge box's recent log lines, or restart its agent program (not the machine). A restart needs a second admin to approve it, and the box only obeys if its own config file has <code>remote_restart: true</code> and a service manager starts the agent again.</p>
        <select value={opGw} onChange={(e) => { setOpGw(e.target.value); setShowLog(''); }} aria-label="Edge box">
          <option value="">Pick an edge box</option>
          {gws.filter((g) => g.kind === 'edge' && g.status === 'active').map((g) => <option key={g.id} value={g.id}>{g.serial} ({siteName(g.site_id)})</option>)}
        </select>{' '}
        <button className="secondary" disabled={!opGw} onClick={() => run(() => post(`/v1/gateways/${opGw}/ops`, { kind: 'logs', lines: 200 }), 'Log request sent.').then(loadOps)}>Get recent log lines</button>{' '}
        <button className="secondary" disabled={!opGw} onClick={() => { if (window.confirm('Ask this box to restart its agent? Another admin must approve it.')) run(() => post(`/v1/gateways/${opGw}/ops`, { kind: 'restart' }), 'Restart requested. Another admin must approve it.').then(loadOps); }}>Request agent restart</button>
        {opGw && <table><thead><tr><th>When</th><th>What</th><th>State</th><th>By</th><th /></tr></thead><tbody>
          {ops.map((o) => <tr key={o.id}><td>{new Date(o.created_at).toLocaleString()}</td><td>{o.kind === 'logs' ? 'Log lines' : 'Agent restart'}</td>
            <td style={{ color: tone(o.status === 'requested' ? 'running' : o.status === 'pending_approval' ? 'pending' : o.status) }}>{o.status.replace('_', ' ')}{o.result?.detail ? ` - ${o.result.detail}` : ''}</td>
            <td>{o.requested_by}{o.approved_by ? `, approved by ${o.approved_by}` : ''}</td>
            <td>{o.status === 'pending_approval' && <button className="secondary" onClick={() => approveOp(o.id)}>Approve restart</button>}
              {o.kind === 'logs' && o.result?.lines && <button className="secondary" onClick={() => setShowLog(showLog === o.id ? '' : o.id)}>{showLog === o.id ? 'Hide' : 'Show'}</button>}</td></tr>)}
          {!ops.length && <tr><td colSpan={5} className="muted">Nothing requested for this box yet.</td></tr>}
        </tbody></table>}
        {showLog && <pre style={{ maxHeight: 320, overflow: 'auto', background: '#111', color: '#ddd', padding: 8, fontSize: 12 }}>{(ops.find((o) => o.id === showLog)?.result?.lines ?? []).join('\n')}</pre>}
      </div>

      <div className="card" style={{ marginTop: 12 }}>
        <b>Device templates</b>
        <p className="muted">Send this box its device list (each device's connection settings plus the register map from its profile) as a signed, numbered update. The box checks it, keeps the old list, restarts its agent, and puts the old list back by itself if it does not reconnect within 3 minutes. Only the device list is sent, never network, certificate or login settings. The box must have <code>managed_devices: true</code> and <code>fleet_public_key</code> in its own config, and the server needs <code>FLEET_SIGNING_KEY</code>. Uses the box picked above.</p>
        <button className="secondary" disabled={!opGw} onClick={() => { if (window.confirm('Send the current device list to this box? Its agent restarts to use it.')) run(() => post(`/v1/gateways/${opGw}/config-push`, {}), 'Device list sent. Watch the state below.').then(loadPushes); }}>Push device templates</button>
        {opGw && <table><thead><tr><th>When</th><th>Version</th><th>State</th><th>By</th></tr></thead><tbody>
          {pushes.map((p) => <tr key={p.id}><td>{new Date(p.created_at).toLocaleString()}</td><td>{p.version}</td>
            <td style={{ color: p.status === 'confirmed' ? '#16a34a' : p.status === 'sent' || p.status === 'applied' ? '#2563eb' : '#dc2626' }}>{p.status.replace('_', ' ')}{p.detail ? ` - ${p.detail}` : ''}</td><td>{p.requested_by}</td></tr>)}
          {!pushes.length && <tr><td colSpan={4} className="muted">Nothing pushed to this box yet.</td></tr>}
        </tbody></table>}
      </div>

      <div className="card" style={{ marginTop: 12 }}>
        <b>Rollouts</b>
        <table><thead><tr><th>Name</th><th>State</th><th>Stage</th><th>Progress</th><th /></tr></thead><tbody>
          {camps.map((c) => {
            const p = progress(c); const acts = allowedActions(c);
            return <tr key={c.id}><td>{c.name}</td><td style={{ color: tone(c.state) }}>{c.state}</td>
              <td>{c.stage_index < 0 ? 'not started' : `${c.stage_index + 1} of ${c.stages.length} (${c.stages[c.stage_index]}%)`}</td>
              <td style={{ minWidth: 180 }}><div style={{ height: 8, background: '#e5e7eb', borderRadius: 4 }}><div style={{ height: 8, width: `${p.pct}%`, background: '#16a34a', borderRadius: 4 }} /></div><span className="muted">{c.acked} updated, {c.open} waiting, <span style={{ color: c.failed ? '#dc2626' : undefined }}>{c.failed} failed</span></span></td>
              <td>{acts.map((a) => <button key={a} className="secondary" style={{ marginRight: 4 }} onClick={() => {
                if (a === 'rollback') {
                  const to = rels.find((r) => r.id !== rel) ?? rels[0];
                  if (!to || !window.confirm(`Move these boxes back to release ${to.version}?`)) return;
                  run(() => post(`/v1/fleet/campaigns/${c.id}/rollback`, { to_release_id: to.id }), `Rollback to ${to.version} started.`);
                } else if (a === 'abort' && !window.confirm('Abort this rollout?')) return;
                else run(() => post(`/v1/fleet/campaigns/${c.id}/${a}`), `${a} done.`);
              }}>{a}</button>)}</td></tr>;
          })}
          {!camps.length && <tr><td colSpan={5} className="muted">No rollouts yet.</td></tr>}
        </tbody></table>
        <p className="muted">Honest status: the rollout engine and edge verify/ack path are unit-tested; no rollout has run on a real edge box. Rollback uses the first other release in the list as target (a picker is not built yet).</p>
      </div>
    </div>
  );
}
