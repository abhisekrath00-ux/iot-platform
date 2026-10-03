import { useEffect, useState } from 'react';
import { api } from '../lib/api';

export type AttrValue = string | number | boolean;

// "name=value" lines <-> attributes. Numbers and true/false are kept as such, anything else is text.
export function parseAttributes(text: string): Record<string, AttrValue> {
  const out: Record<string, AttrValue> = {};
  for (const line of text.split('\n')) {
    const t = line.trim();
    if (!t) continue;
    const i = t.indexOf('=');
    if (i < 1) throw new Error(`Line "${t}" needs the form name=value`);
    const k = t.slice(0, i).trim(), v = t.slice(i + 1).trim();
    out[k] = v === 'true' ? true : v === 'false' ? false : v !== '' && !Number.isNaN(Number(v)) ? Number(v) : v;
  }
  return out;
}

export function formatAttributes(a: Record<string, AttrValue>): string {
  return Object.entries(a).map(([k, v]) => `${k}=${v}`).join('\n');
}

// Device attributes: labels such as owner, floor or serial. Metadata only, never sent to the device.
export default function DeviceAttributes({ deviceId, attributes, onSaved, kind = 'device' }: { deviceId: string; attributes: Record<string, AttrValue>; onSaved: () => void; kind?: 'device' | 'asset' }) {
  const [text, setText] = useState(formatAttributes(attributes));
  const [dirty, setDirty] = useState(false);
  const [msg, setMsg] = useState('');
  useEffect(() => { if (!dirty) setText(formatAttributes(attributes)); }, [attributes, dirty]);
  async function save() {
    setMsg('');
    try {
      const parsed = parseAttributes(text);
      await api(`/v1/${kind === 'asset' ? 'assets' : 'devices'}/${deviceId}/attributes`, { method: 'PUT', body: JSON.stringify({ attributes: parsed }) });
      setDirty(false); setMsg('Saved.'); onSaved();
    } catch (e) { setMsg(String(e instanceof Error ? e.message : e)); }
  }
  return (
    <div className="card" style={{ maxWidth: 520, marginTop: 16 }} role="region" aria-label={`${kind === 'asset' ? 'Asset' : 'Device'} attributes`}>
      <b>Attributes</b>
      <p className="muted">Labels for this {kind}, one name=value per line (for example owner=plant team, floor=2). They are notes for people and reports; they are never sent to the device. Operators and admins can edit.</p>
      <textarea aria-label="Attributes" rows={4} style={{ width: '100%' }} value={text} placeholder="owner=plant team" onChange={e => { setText(e.target.value); setDirty(true); }} />
      <div style={{ marginTop: 8 }}><button onClick={save} disabled={!dirty}>Save attributes</button></div>
      {msg && <p className="muted" role="status">{msg}</p>}
    </div>
  );
}
