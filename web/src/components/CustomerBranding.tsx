import { useEffect, useState } from 'react';
import { api } from '../lib/api';

interface CB { product_name: string; accent: string; has_logo: boolean; }

// Admin editor for one customer's branding override. Users scoped to the
// customer see these instead of the tenant branding; empty fields fall back
// to the tenant values.
export default function CustomerBranding({ id, name }: { id: string; name: string }) {
  const [b, setB] = useState<CB | null>(null);
  const [pname, setPname] = useState('');
  const [accent, setAccent] = useState('');
  const [msg, setMsg] = useState('');
  const [logoUrl, setLogoUrl] = useState('');
  const load = () => api<CB>(`/v1/customers/${id}/branding`).then(r => {
    setB(r); setPname(r.product_name); setAccent(r.accent);
    if (r.has_logo) {
      const token = localStorage.getItem('iot.token');
      fetch(`/v1/customers/${id}/branding/logo`, { headers: token ? { Authorization: `Bearer ${token}` } : {} })
        .then(res => (res.ok ? res.blob() : null)).then(bl => { if (bl) setLogoUrl(u => { if (u) URL.revokeObjectURL(u); return URL.createObjectURL(bl); }); }).catch(() => {});
    } else setLogoUrl(u => { if (u) URL.revokeObjectURL(u); return ''; });
  }).catch(e => setMsg(String(e)));
  useEffect(() => { load(); }, [id]);

  async function save(e: React.FormEvent) {
    e.preventDefault(); setMsg('');
    try { await api(`/v1/customers/${id}/branding`, { method: 'PUT', body: JSON.stringify({ product_name: pname, accent }) }); setMsg('Saved.'); load(); }
    catch (e2) { setMsg(String(e2)); }
  }
  async function upload(f: File) {
    setMsg('');
    try {
      const token = localStorage.getItem('iot.token');
      const r = await fetch(`/v1/customers/${id}/branding/logo`, { method: 'PUT', body: f, headers: token ? { Authorization: `Bearer ${token}` } : {} });
      if (!r.ok) throw new Error(`${r.status}: ${await r.text()}`);
      setMsg('Logo saved.'); load();
    } catch (e) { setMsg(String(e)); }
  }
  async function clearLogo() {
    setMsg('');
    try { await api(`/v1/customers/${id}/branding/logo`, { method: 'DELETE' }); setMsg('Logo removed; the tenant logo shows again.'); load(); }
    catch (e) { setMsg(String(e)); }
  }
  if (!b) return null;
  return (
    <div style={{ width: '100%', borderTop: '1px solid var(--border, #e5e5e5)', marginTop: 8, paddingTop: 8 }}>
      <b>Branding for {name}</b>
      <p className="muted">Shown to users scoped to this customer instead of the workspace branding. Empty fields keep the workspace values.</p>
      <form onSubmit={save} style={{ display: 'flex', gap: 10, flexWrap: 'wrap', alignItems: 'flex-end' }}>
        <span><label htmlFor={`cb-n-${id}`}>Product name</label>
          <input id={`cb-n-${id}`} value={pname} maxLength={40} onChange={e => setPname(e.target.value)} placeholder="Workspace name" style={{ maxWidth: 220 }} /></span>
        <span><label htmlFor={`cb-a-${id}`}>Accent (#rrggbb)</label>
          <input id={`cb-a-${id}`} value={accent} onChange={e => setAccent(e.target.value)} placeholder="#0071e3" pattern="#[0-9a-fA-F]{6}|" style={{ maxWidth: 140 }} /></span>
        <span><label htmlFor={`cb-l-${id}`}>Logo (PNG/JPEG)</label>
          <input id={`cb-l-${id}`} type="file" accept="image/png,image/jpeg" onChange={e => { const f = e.target.files?.[0]; if (f) upload(f); }} style={{ maxWidth: 220 }} /></span>
        <button type="submit">Save</button>
        {b.has_logo && <button type="button" className="ghost" onClick={clearLogo}>Remove logo</button>}
        {logoUrl && <img src={logoUrl} alt={`${name} logo`} style={{ height: 28, width: 28, objectFit: 'contain', borderRadius: 6 }} />}
      </form>
      {msg && <p className="muted" role="status">{msg}</p>}
    </div>
  );
}
