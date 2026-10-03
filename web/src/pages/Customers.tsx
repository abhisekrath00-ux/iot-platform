import { useEffect, useState } from 'react';
import Empty from '../components/Empty';
import { api, Device } from '../lib/api';

interface Customer { id: string; parent_id: string | null; name: string; devices: number; users: string[]; }

export default function Customers() {
  const [rows, setRows] = useState<Customer[]>([]);
  const [devices, setDevices] = useState<Device[]>([]);
  const [name, setName] = useState('');
  const [parent, setParent] = useState('');
  const [devId, setDevId] = useState('');
  const [custId, setCustId] = useState('');
  const [userId, setUserId] = useState('');
  const [msg, setMsg] = useState('');

  const load = () => {
    api<Customer[]>('/v1/customers').then(setRows).catch(e => setMsg(String(e)));
    api<Device[]>('/v1/devices').then(setDevices).catch(() => {});
  };
  useEffect(load, []);
  const act = async (fn: () => Promise<unknown>, ok: string) => {
    setMsg('');
    try { await fn(); setMsg(ok); load(); } catch (e) { setMsg(String(e)); }
  };
  const kids = (p: string | null) => rows.filter(c => c.parent_id === p);
  const Node = ({ c, depth }: { c: Customer; depth: number }) => (
    <li style={{ marginLeft: depth ? 18 : 0, listStyle: 'none' }}>
      <div className="card" style={{ margin: '6px 0', padding: '10px 14px', display: 'flex', gap: 10, alignItems: 'center', flexWrap: 'wrap' }}>
        <b>{c.name}</b>
        <span className="muted">{c.devices} device{c.devices === 1 ? '' : 's'}</span>
        {c.users.map(u => (
          <span key={u} className="pill">{u} <button className="ghost" aria-label={`Remove ${u} from ${c.name}`} onClick={() => act(() => api(`/v1/customers/${c.id}/users/${encodeURIComponent(u)}`, { method: 'DELETE' }), 'User is tenant-wide again.')}>x</button></span>
        ))}
        <span style={{ flex: 1 }} />
        <button className="ghost" aria-label={`Delete ${c.name}`} onClick={() => act(() => api(`/v1/customers/${c.id}`, { method: 'DELETE' }), 'Deleted.')}>Delete</button>
      </div>
      <ul style={{ padding: 0, margin: 0 }}>{kids(c.id).map(k => <Node key={k.id} c={k} depth={depth + 1} />)}</ul>
    </li>
  );
  return (
    <>
      <h1>Customers</h1>
      <p className="muted">Customers and sub-customers inside this tenant. A user scoped to a customer sees only the devices of that customer and its sub-customers (read-only: devices, latest and historic values, alerts). Everything else is refused for them. Admin session only.</p>
      {msg && <p className="muted" role="status">{msg}</p>}
      {rows.length === 0 ? <Empty title="No customers yet" hint="Create a customer, assign devices, then scope a user to it." />
        : <ul style={{ padding: 0 }}>{kids(null).map(c => <Node key={c.id} c={c} depth={0} />)}</ul>}
      <div className="card" style={{ maxWidth: 640, marginBottom: 20 }}>
        <b>New customer</b>
        <form onSubmit={e => { e.preventDefault(); act(() => api('/v1/customers', { method: 'POST', body: JSON.stringify({ name, parent_id: parent || null }) }), 'Created.').then(() => setName('')); }}>
          <label htmlFor="cu-name">Name</label>
          <input id="cu-name" value={name} onChange={e => setName(e.target.value)} required />
          <label htmlFor="cu-parent">Parent</label>
          <select id="cu-parent" value={parent} onChange={e => setParent(e.target.value)}><option value="">(top level)</option>{rows.map(c => <option key={c.id} value={c.id}>{c.name}</option>)}</select>
          <button type="submit">Create</button>
        </form>
      </div>
      <div className="card" style={{ maxWidth: 640, marginBottom: 20 }}>
        <b>Assign a device</b>
        <label htmlFor="cu-dev">Device</label>
        <select id="cu-dev" value={devId} onChange={e => setDevId(e.target.value)}><option value="">Choose</option>{devices.map(d => <option key={d.id} value={d.id}>{d.name || d.id}</option>)}</select>
        <label htmlFor="cu-c">Customer</label>
        <select id="cu-c" value={custId} onChange={e => setCustId(e.target.value)}><option value="">(none)</option>{rows.map(c => <option key={c.id} value={c.id}>{c.name}</option>)}</select>
        <button disabled={!devId} onClick={() => act(() => api(`/v1/devices/${devId}/customer`, { method: 'PUT', body: JSON.stringify({ customer_id: custId || null }) }), 'Assigned.')}>Assign</button>
      </div>
      <div className="card" style={{ maxWidth: 640 }}>
        <b>Scope a user to a customer</b>
        <label htmlFor="cu-user">User id (the login subject)</label>
        <input id="cu-user" value={userId} onChange={e => setUserId(e.target.value)} />
        <button disabled={!userId || !custId} onClick={() => act(() => api(`/v1/customers/${custId}/users/${encodeURIComponent(userId)}`, { method: 'PUT' }), 'User scoped. They can no longer see anything outside this customer.')}>Scope to the customer chosen above</button>
      </div>
    </>
  );
}
