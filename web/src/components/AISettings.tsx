import { useEffect, useState } from 'react';
import { api } from '../lib/api';

interface S { enabled: boolean; base_url: string; model: string; has_key: boolean; secrets_available: boolean; }

// Connect a model for the assistant: any OpenAI-compatible endpoint, hosted (OpenAI and others) or
// local (Ollama, llama.cpp, vLLM). The key is write-only and stored encrypted.
export default function AISettings() {
  const [s, setS] = useState<S | null>(null);
  const [url, setUrl] = useState('');
  const [model, setModel] = useState('');
  const [key, setKey] = useState('');
  const [enabled, setEnabled] = useState(false);
  const [msg, setMsg] = useState('');
  const load = () => api<S>('/v1/ai/settings').then(r => { setS(r); setUrl(r.base_url); setModel(r.model); setEnabled(r.enabled); }).catch(() => undefined);
  useEffect(() => { load(); }, []);
  async function save(clearKey = false) {
    setMsg('');
    try {
      await api('/v1/ai/settings', { method: 'PUT', body: JSON.stringify({ enabled, base_url: url, model, ...(key ? { api_key: key } : {}), clear_key: clearKey }) });
      setKey(''); setMsg('Saved.'); load();
    } catch (e) { setMsg(String(e)); }
  }
  async function test() {
    setMsg('Testing...');
    try {
      const r = await api<{ ok: boolean; reply?: string; error?: string }>('/v1/ai/test', { method: 'POST' });
      setMsg(r.ok ? `The model answered: "${r.reply}"` : `Test failed: ${r.error}`);
    } catch (e) { setMsg(String(e)); }
  }
  if (!s) return null;
  return (
    <div className="card" style={{ maxWidth: 760, marginTop: 20 }}>
      <b>AI assistant model</b>
      <p className="muted">Connect any OpenAI-compatible model: a hosted API, or a local server such as Ollama (http://host:11434/v1), llama.cpp or vLLM. Nothing leaves the site unless you point it elsewhere. The assistant acts as the signed-in user, changes need that user's confirmation, and it can never approve control commands. Off by default. Admins only.</p>
      <label className="muted" style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
        <input type="checkbox" style={{ width: 'auto', flex: 'none' }} checked={enabled} onChange={e => setEnabled(e.target.checked)} /> Enable the assistant
      </label>
      <label htmlFor="ai-url">Base URL</label>
      <input id="ai-url" placeholder="http://localhost:11434/v1" value={url} onChange={e => setUrl(e.target.value)} />
      <label htmlFor="ai-model">Model name</label>
      <input id="ai-model" placeholder="llama3.1 or gpt-4o" value={model} onChange={e => setModel(e.target.value)} />
      <label htmlFor="ai-key">API key {s.has_key && <span className="pill ok">stored</span>} <span className="muted">(optional for local servers; never shown again)</span></label>
      <input id="ai-key" type="password" autoComplete="off" placeholder={s.has_key ? 'leave empty to keep the stored key' : 'sk-...'} value={key} onChange={e => setKey(e.target.value)} disabled={!s.secrets_available} />
      {!s.secrets_available && <p className="muted" style={{ fontSize: 12 }}>Storing a key needs SECRETS_KEY on the server. Local servers without a key work without it.</p>}
      <div style={{ display: 'flex', gap: 8, marginTop: 12 }}>
        <button onClick={() => save()}>Save</button>
        <button className="ghost" onClick={test}>Test connection</button>
        {s.has_key && <button className="ghost" onClick={() => save(true)}>Remove key</button>}
      </div>
      {msg && <p className="muted" role="status">{msg}</p>}
    </div>
  );
}
