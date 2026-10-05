import { useState } from 'react';
import { api } from '../lib/api';

export interface SiteOpt { id: string; name: string; }

// Site picker with an inline "Create site". A new workspace may have no site yet; without one the
// forms that need a site cannot be submitted, so the empty state explains it and offers to create one.
export function SiteSelect({ sites, site, setSite, onCreated }: { sites: SiteOpt[]; site: string; setSite: (id: string) => void; onCreated: (s: SiteOpt) => void }) {
  const [open, setOpen] = useState(false);
  const [name, setName] = useState('');
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState('');
  const showForm = open || sites.length === 0;

  async function create() {
    if (!name.trim()) { setErr('Enter a name for the site, for example "Main plant".'); return; }
    setBusy(true); setErr('');
    try {
      const s = await api<SiteOpt>('/v1/sites', { method: 'POST', body: JSON.stringify({ name: name.trim() }) });
      onCreated(s); setSite(s.id); setName(''); setOpen(false);
    } catch (e) {
      const m = String(e);
      setErr(/403|forbidden|admin/i.test(m) ? 'Only an administrator can create sites. Ask one to create a site, then reload.' : m);
    } finally { setBusy(false); }
  }

  return (
    <div>
      <label>Site</label>
      {sites.length > 0 && (
        <select value={site} onChange={e => setSite(e.target.value)} required>
          {sites.map(s => <option key={s.id} value={s.id}>{s.name}</option>)}
        </select>
      )}
      {sites.length === 0 && <p className="muted" style={{ margin: '4px 0' }}>You have no site yet. A site is a plant, building or location where your gateways and devices live. Create your first one:</p>}
      {sites.length > 0 && !open && <div style={{ marginTop: 6 }}><button type="button" className="link" onClick={() => setOpen(true)}>+ New site</button></div>}
      {showForm && (
        <div style={{ display: 'flex', gap: 8, marginTop: 6, flexWrap: 'wrap' }}>
          <input value={name} onChange={e => setName(e.target.value)} placeholder="Main plant" aria-label="New site name" maxLength={80}
            onKeyDown={e => { if (e.key === 'Enter') { e.preventDefault(); void create(); } }} />
          <button type="button" onClick={() => void create()} disabled={busy}>{busy ? 'Creating...' : 'Create site'}</button>
        </div>
      )}
      {err && <p role="alert" style={{ color: '#b91c1c', margin: '6px 0' }}>{err}</p>}
    </div>
  );
}
