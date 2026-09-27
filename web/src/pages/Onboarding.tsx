import { useState } from 'react';
import { api } from '../lib/api';

// Guided installer path: site -> gateway -> profile -> port/wiring test ->
// live preview -> name -> commission. This first cut registers the device;
// the port test and live preview steps call the gateway agent next.
export default function Onboarding() {
  const [gateway, setGateway] = useState('demo-gw');
  const [profile, setProfile] = useState('modbus-energy-meter');
  const [name, setName] = useState('');
  const [port, setPort] = useState('/dev/ttyUSB0');
  const [msg, setMsg] = useState('');

  async function submit(e: React.FormEvent) {
    e.preventDefault();
    setMsg('');
    try {
      const r = await api<{ id: string }>('/v1/devices', {
        method: 'POST',
        body: JSON.stringify({ gateway_id: gateway, profile, name, config: { port } })
      });
      setMsg(`Device registered (${r.id}). Gateway picks up config on next sync.`);
    } catch (e2) { setMsg(String(e2)); }
  }

  return (
    <>
      <h1>Add device</h1>
      <form onSubmit={submit}>
        <label>Gateway</label>
        <input value={gateway} onChange={e => setGateway(e.target.value)} required />
        <label>Device profile</label>
        <select value={profile} onChange={e => setProfile(e.target.value)}>
          <option value="modbus-energy-meter">Energy meter (Modbus RTU)</option>
          <option value="door-contact">Door contact</option>
        </select>
        <label>Serial port</label>
        <input value={port} onChange={e => setPort(e.target.value)} required />
        <label>Display name</label>
        <input value={name} onChange={e => setName(e.target.value)} placeholder="Main energy meter" required />
        <div style={{ marginTop: 16 }}><button type="submit">Register device</button></div>
      </form>
      {msg && <p className="muted">{msg}</p>}
    </>
  );
}
