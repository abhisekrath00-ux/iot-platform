import { useEffect, useState } from 'react';
import { api } from '../lib/api';

interface P { id: string; name: string; base_url: string; model: string; has_key: boolean; builtin: boolean; active: boolean; capability: string; small_model_mode: boolean; }
interface L { profiles: P[]; enabled: boolean; secrets_available: boolean; backups?: string[]; }
// Fills the base URL only; every one of these speaks the OpenAI-compatible chat API. Anthropic
// documents its compatibility layer as for testing and comparing, not as a production path.
const PRESETS: { label: string; url: string }[] = [
  { label: 'OpenAI', url: 'https://api.openai.com/v1' },
  { label: 'Anthropic (compatibility layer)', url: 'https://api.anthropic.com/v1/' },
  { label: 'Google Gemini (compatibility)', url: 'https://generativelanguage.googleapis.com/v1beta/openai/' },
  { label: 'OpenRouter', url: 'https://openrouter.ai/api/v1' },
];

// Saved AI provider profiles: add each provider once (key stored encrypted, never shown again),
// then switch the active one with a click. The bundled local model is always listed.
export default function AIProfiles({ onChange }: { onChange?: () => void }) {
  const [l, setL] = useState<L | null>(null);
  const [edit, setEdit] = useState<string | null>(null); // profile id, 'new' or null
  const [name, setName] = useState('');
  const [url, setUrl] = useState('');
  const [model, setModel] = useState('');
  const [key, setKey] = useState('');
  const [cap, setCap] = useState('auto');
  const [msg, setMsg] = useState('');
  const load = () => api<L>('/v1/ai/profiles').then(setL).catch(() => undefined);
  useEffect(() => { load(); }, []);
  function open(p?: P) {
    setEdit(p ? p.id : 'new'); setName(p?.name ?? ''); setUrl(p?.base_url ?? ''); setModel(p?.model ?? ''); setCap(p?.capability ?? 'auto'); setKey(''); setMsg('');
  }
  async function run(path: string, init: RequestInit, ok?: string) {
    setMsg('');
    try { const r = await api<L>(path, init); setL(r); if (ok) setMsg(ok); onChange?.(); } catch (e) { setMsg(String(e)); }
  }
  async function save(clearKey = false) {
    const body = JSON.stringify({ name, base_url: url, model, capability: cap, ...(key ? { api_key: key } : {}), clear_key: clearKey });
    await run(edit === 'new' ? '/v1/ai/profiles' : `/v1/ai/profiles/${edit}`, { method: edit === 'new' ? 'POST' : 'PUT', body }, 'Saved.');
    setKey(''); setEdit(null);
  }
  async function test(id: string) {
    setMsg('Testing...');
    try {
      const r = await api<{ ok: boolean; reply?: string; error?: string }>(`/v1/ai/profiles/${id}/test`, { method: 'POST' });
      setMsg(r.ok ? `The model answered: "${r.reply}"` : `Test failed: ${r.error}`);
    } catch (e) { setMsg(String(e)); }
  }
  const backups = l?.backups ?? [];
  const saveBackups = (ids: string[]) => run('/v1/ai/backups', { method: 'PUT', body: JSON.stringify({ ids }) }, 'Backup order saved.');
  const move = (id: string, d: number) => { const a = [...backups], i = a.indexOf(id), j = i + d; if (i < 0 || j < 0 || j >= a.length) return; [a[i], a[j]] = [a[j], a[i]]; saveBackups(a); };
  if (!l) return null;
  const active = l.profiles.find(p => p.active);
  return (
    <div className="card" style={{ maxWidth: 760, marginTop: 20 }}>
      <b>AI providers</b>
      <p className="muted">Save several providers and switch between them without typing keys again. Keys are stored encrypted on the server and never shown. Admins only; every change is audited.</p>
      <label htmlFor="ai-active">Active provider</label>
      <select id="ai-active" value={active?.id ?? ''} onChange={e => e.target.value && run(`/v1/ai/profiles/${e.target.value}/activate`, { method: 'POST' }, 'Switched.')}>
        {!active && <option value="">None active (assistant off)</option>}
        {l.profiles.map(p => <option key={p.id} value={p.id}>{p.name} - {p.model}</option>)}
      </select>
      <table style={{ width: '100%', marginTop: 12 }}>
        <thead><tr><th>Name</th><th>Model</th><th>Mode</th><th>Key</th><th /></tr></thead>
        <tbody>
          {l.profiles.map(p => (
            <tr key={p.id}>
              <td>{p.name} {p.active && <span className="pill ok">active</span>}{backups.includes(p.id) && <span className="pill"> backup {backups.indexOf(p.id) + 1}</span>}</td>
              <td>{p.model}</td>
              <td>{p.small_model_mode ? 'small model' : 'full'}</td>
              <td>{p.builtin ? 'none needed' : p.has_key ? 'stored' : 'none'}</td>
              <td><div style={{ display: 'flex', flexWrap: 'wrap', gap: 4 }}>
                {!p.active && <button className="ghost" onClick={() => run(`/v1/ai/profiles/${p.id}/activate`, { method: 'POST' }, 'Switched.')}>Use</button>}{' '}
                <button className="ghost" onClick={() => test(p.id)}>Test</button>
                {!p.active && (backups.includes(p.id)
                  ? <>{' '}<button className="ghost" aria-label={`Move ${p.name} up`} onClick={() => move(p.id, -1)}>Up</button>{' '}<button className="ghost" aria-label={`Move ${p.name} down`} onClick={() => move(p.id, 1)}>Down</button>{' '}<button className="ghost" onClick={() => saveBackups(backups.filter(x => x !== p.id))}>Remove</button></>
                  : backups.length < 3 && <>{' '}<button className="ghost" onClick={() => saveBackups([...backups, p.id])}>Use as backup</button></>)}
                {!p.builtin && <>{' '}<button className="ghost" onClick={() => open(p)}>Edit</button>{' '}
                  <button className="ghost" disabled={p.active} title={p.active ? 'Switch to another provider first' : ''} onClick={() => window.confirm(`Delete "${p.name}" and its stored key?`) && run(`/v1/ai/profiles/${p.id}`, { method: 'DELETE' }, 'Deleted.')}>Delete</button></>}
              </div></td>
            </tr>
          ))}
        </tbody>
      </table>
      <p className="muted" style={{ fontSize: 12, marginTop: 8 }}>Backups are tried in order, only when the active provider is unreachable, rejects the key, hits a rate or quota limit or returns a server error. A hosted backup receives the conversation, so add one only if that is acceptable. Up to 3.</p>
      {edit === null && <div style={{ marginTop: 12 }}><button onClick={() => open()}>Add provider</button></div>}
      {edit !== null && (
        <div style={{ marginTop: 12 }}>
          <label htmlFor="pf-name">Name</label>
          <input id="pf-name" placeholder="OpenRouter, Work OpenAI..." value={name} onChange={e => setName(e.target.value)} />
          <label htmlFor="pf-preset">Start from</label>
          <select id="pf-preset" value="" onChange={e => { const x = PRESETS.find(q => q.label === e.target.value); if (x) { setUrl(x.url); if (!name) setName(x.label.split(' (')[0]); } }}>
            <option value="">Choose a provider (fills the base URL)</option>
            {PRESETS.map(q => <option key={q.label} value={q.label}>{q.label}</option>)}
          </select>
          <label htmlFor="pf-url">Base URL</label>
          <input id="pf-url" placeholder="https://openrouter.ai/api/v1" value={url} onChange={e => setUrl(e.target.value)} />
          <label htmlFor="pf-model">Model name</label>
          <input id="pf-model" placeholder="gpt-4o" value={model} onChange={e => setModel(e.target.value)} />
          <label htmlFor="pf-cap">Model capability</label>
          <select id="pf-cap" value={cap} onChange={e => setCap(e.target.value)}>
            <option value="auto">Auto (local runtime = small, hosted = full)</option>
            <option value="small">Small (compact prompt, short tool menu, fixed answers)</option>
            <option value="full">Full (complete prompt and all tools, for bigger models)</option>
          </select>
          <label htmlFor="pf-key">API key <span className="muted">(never shown again)</span></label>
          <input id="pf-key" type="password" autoComplete="off" placeholder={edit !== 'new' ? 'leave empty to keep the stored key' : 'sk-...'} value={key} onChange={e => setKey(e.target.value)} disabled={!l.secrets_available} />
          {!l.secrets_available && <p className="muted" style={{ fontSize: 12 }}>Storing a key needs SECRETS_KEY on the server.</p>}
          <div style={{ display: 'flex', gap: 8, marginTop: 12 }}>
            <button onClick={() => save()}>Save</button>
            {edit !== 'new' && <button className="ghost" onClick={() => save(true)}>Remove key</button>}
            <button className="ghost" onClick={() => setEdit(null)}>Cancel</button>
          </div>
        </div>
      )}
      {msg && <p className="muted" role="status">{msg}</p>}
    </div>
  );
}
