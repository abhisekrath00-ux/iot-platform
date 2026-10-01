import { useEffect, useState } from 'react';
import Empty from '../components/Empty';
import { api, Device } from '../lib/api';

interface Asset { id: string; parent_id: string | null; name: string; kind: string; devices: number; }

export default function Assets() {
  const [assets, setAssets] = useState<Asset[]>([]);
  const [devices, setDevices] = useState<Device[]>([]);
  const [name, setName] = useState('');
  const [kind, setKind] = useState('plant');
  const [parent, setParent] = useState('');
  const [devId, setDevId] = useState('');
  const [assetId, setAssetId] = useState('');
  const [msg, setMsg] = useState('');

  const load = () => {
    api<Asset[]>('/v1/assets').then(setAssets).catch(e => setMsg(String(e)));
    api<Device[]>('/v1/devices').then(setDevices).catch(() => {});
  };
  useEffect(load, []);

  const act = async (fn: () => Promise<unknown>, ok: string) => {
    setMsg('');
    try { await fn(); setMsg(ok); load(); } catch (e) { setMsg(String(e)); }
  };

  const children = (p: string | null) => assets.filter(a => a.parent_id === p);
  const Node = ({ a, depth }: { a: Asset; depth: number }) => (
    <li style={{ marginLeft: depth ? 18 : 0, listStyle: 'none' }}>
      <div className="card" style={{ margin: '6px 0', padding: '10px 14px', display: 'flex', gap: 10, alignItems: 'center' }}>
        <b>{a.name}</b><span className="pill">{a.kind}</span>
        <span className="muted">{a.devices} device{a.devices === 1 ? '' : 's'}</span>
        <span style={{ flex: 1 }} />
        <button className="ghost" aria-label={`Delete ${a.name}`} onClick={() => act(() => api(`/v1/assets/${a.id}`, { method: 'DELETE' }), 'Deleted.')}>Delete</button>
      </div>
      <ul style={{ padding: 0, margin: 0 }}>{children(a.id).map(c => <Node key={c.id} a={c} depth={depth + 1} />)}</ul>
    </li>
  );

  return (
    <>
      <h1>Assets</h1>
      <p className="muted">Organise devices as plant, line, machine. Deleting needs the asset to be empty.</p>
      {msg && <p className="muted" role="status">{msg}</p>}
      {assets.length === 0
        ? <Empty title="No assets yet" hint="Create a plant first, then lines and machines under it." />
        : <ul style={{ padding: 0 }}>{children(null).map(a => <Node key={a.id} a={a} depth={0} />)}</ul>}
      <div className="card" style={{ maxWidth: 640, marginBottom: 20 }}>
        <b>New asset</b>
        <form onSubmit={e => { e.preventDefault(); act(() => api('/v1/assets', { method: 'POST', body: JSON.stringify({ name, kind, parent_id: parent || null }) }), 'Created.').then(() => setName('')); }}>
          <label htmlFor="as-name">Name</label>
          <input id="as-name" value={name} onChange={e => setName(e.target.value)} placeholder="Boiler house" required />
          <label htmlFor="as-kind">Kind</label>
          <select id="as-kind" value={kind} onChange={e => setKind(e.target.value)}>
            {['plant', 'line', 'machine', 'room', 'asset'].map(k => <option key={k}>{k}</option>)}
          </select>
          <label htmlFor="as-parent">Parent (optional)</label>
          <select id="as-parent" value={parent} onChange={e => setParent(e.target.value)}>
            <option value="">(top level)</option>
            {assets.map(a => <option key={a.id} value={a.id}>{a.name}</option>)}
          </select>
          <div style={{ marginTop: 14 }}><button type="submit">Create asset</button></div>
        </form>
      </div>
      <div className="card" style={{ maxWidth: 640, marginBottom: 20 }}>
        <b>Attach a device</b>
        <label htmlFor="as-dev">Device</label>
        <select id="as-dev" value={devId} onChange={e => setDevId(e.target.value)}>
          <option value="">Select a device</option>
          {devices.map(d => <option key={d.id} value={d.id}>{d.name}</option>)}
        </select>
        <label htmlFor="as-asset">Asset</label>
        <select id="as-asset" value={assetId} onChange={e => setAssetId(e.target.value)}>
          <option value="">(detach)</option>
          {assets.map(a => <option key={a.id} value={a.id}>{a.name}</option>)}
        </select>
        <div style={{ marginTop: 14 }}>
          <button disabled={!devId} onClick={() => act(() => api(`/v1/devices/${devId}/asset`, { method: 'PUT', body: JSON.stringify({ asset_id: assetId || null }) }), 'Saved.')}>Save</button>
        </div>
      </div>
    </>
  );
}
