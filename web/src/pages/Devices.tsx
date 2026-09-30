import { useEffect, useState } from 'react';
import { Link } from 'react-router-dom';
import { api, Device } from '../lib/api';
import Empty from '../components/Empty';

export default function Devices() {
  const [devices, setDevices] = useState<Device[]>([]);
  const [err, setErr] = useState('');
  useEffect(() => { api<Device[]>('/v1/devices').then(setDevices).catch(e => setErr(String(e))); }, []);
  return (
    <>
      <h1>Devices</h1>
      {err && <p className="muted">{err}</p>}
      {devices.length === 0 && !err ? <Empty title="No devices yet" hint="Add a device to start collecting data." action={<Link to="/onboarding"><button>Add a device</button></Link>} /> : (
      <table>
        <thead><tr><th>Name</th><th>Profile</th><th>Gateway</th><th>Added</th></tr></thead>
        <tbody>
          {devices.map(d => (
            <tr key={d.id}>
              <td><Link to={`/devices/${d.id}`} style={{ color: 'var(--accent)' }}>{d.name}</Link></td>
              <td>{d.profile}</td><td>{d.gateway_id}</td>
              <td className="muted">{new Date(d.created_at).toLocaleString()}</td>
            </tr>
          ))}
        </tbody>
      </table>)}
    </>
  );
}
