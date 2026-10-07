import { useEffect, useState } from 'react';
import { api, download } from '../lib/api';

interface Note { id: string; title: string; content: string; created_at: string; expires_at: string; }
interface Memory { enabled: boolean; notes: Note[]; }
export function memoryValid(title: string, content: string, days: number) {
  const bytes = (s: string) => new TextEncoder().encode(s.trim()).length;
  return bytes(title) > 0 && bytes(title) <= 120 && bytes(content) > 0 && bytes(content) <= 2000 && Number.isInteger(days) && days >= 1 && days <= 90;
}
export default function AssistantMemory() {
  const [memory, setMemory] = useState<Memory | null>(null);
  const [error, setError] = useState('');
  const [busy, setBusy] = useState(false);
  const [consent, setConsent] = useState(false);
  const [title, setTitle] = useState('');
  const [content, setContent] = useState('');
  const [days, setDays] = useState(30);
  const [remove, setRemove] = useState<string | null>(null);
  async function refresh() { setMemory(await api<Memory>('/v1/assistant/memory')); }
  useEffect(() => { refresh().catch(e => setError(String(e))); }, []);
  async function work(action: () => Promise<unknown>) {
    setBusy(true); setError('');
    try { await action(); await refresh(); } catch (e) { setError(String(e)); }
    finally { setBusy(false); }
  }
  return <section className="card" aria-label="Private assistant memory" style={{ maxWidth: 900, margin: '24px 0', padding: 24 }}>
    <div style={{ display: 'flex', justifyContent: 'space-between', flexWrap: 'wrap', gap: 12 }}>
      <h2 style={{ margin: 0 }}>Private memory</h2><span className={`pill ${memory?.enabled ? 'ok' : ''}`}>{memory?.enabled ? 'Opted in' : 'Off by default'}</span>
    </div>
    <p className="muted">Save a few notes for the assistant to find later. Only you can manage them. Chat is not saved automatically. Notes never approve changes or override safety rules.</p>
    <div style={{ border: '1px solid var(--line)', borderRadius: 12, padding: 14 }}>
      <b>Before you enable it</b>
      <p className="muted" style={{ marginBottom: 0 }}>If your admin connected a hosted AI provider, notes the assistant retrieves can be sent to that provider. Use a local model for on-premises privacy. Do not save credentials or sensitive information about other people.</p>
    </div>
    {error && <div role="alert"><p>{error}</p><button className="ghost" disabled={busy} onClick={() => work(async () => {})}>Refresh notes</button></div>}
    {!memory && !error && <p className="muted">Loading your notes...</p>}
    {memory && <>
      {!memory.enabled ? <div style={{ marginTop: 16 }}>
        <label style={{ display: 'flex', alignItems: 'flex-start', gap: 8 }}><input type="checkbox" checked={consent} onChange={e => setConsent(e.target.checked)} style={{ width: 18, marginTop: 3 }} />I understand where retrieved notes may be sent and want to enable private memory.</label>
        <button disabled={busy || !consent} style={{ marginTop: 12 }} onClick={() => work(() => api('/v1/assistant/memory/settings', { method: 'PUT', body: JSON.stringify({ enabled: true }) }))}>Enable memory</button>
      </div> : <div style={{ marginTop: 16 }}>
        <button className="ghost" disabled={busy} onClick={() => work(() => api('/v1/assistant/memory/settings', { method: 'PUT', body: JSON.stringify({ enabled: false }) }))}>Disable retrieval</button>
        <p className="muted" style={{ fontSize: 12 }}>Disabling keeps saved notes so you can export or delete them.</p>
        <form onSubmit={e => { e.preventDefault(); work(async () => { await api('/v1/assistant/memory', { method: 'POST', body: JSON.stringify({ title: title.trim(), content: content.trim(), retention_days: days }) }); setTitle(''); setContent(''); }); }}>
          <label htmlFor="memory-title">Note title</label><input id="memory-title" value={title} onChange={e => setTitle(e.target.value)} placeholder="For example: pump label" maxLength={120} disabled={busy} />
          <label htmlFor="memory-content">What should the assistant be able to find?</label><textarea id="memory-content" value={content} onChange={e => setContent(e.target.value)} rows={3} maxLength={2000} style={{ width: '100%', boxSizing: 'border-box' }} disabled={busy} />
          <div style={{ display: 'flex', alignItems: 'end', gap: 12, flexWrap: 'wrap', marginTop: 10 }}>
            <label>Keep for (days)<input aria-label="Memory retention days" type="number" min={1} max={90} value={days} onChange={e => setDays(Number(e.target.value))} style={{ width: 100, display: 'block' }} disabled={busy} /></label>
            <button type="submit" disabled={busy || !memoryValid(title, content, days) || memory.notes.length >= 50}>Save this note</button>
          </div>
        </form>
      </div>}
      <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginTop: 24, gap: 10, flexWrap: 'wrap' }}>
        <h3 style={{ margin: 0 }}>Your saved notes ({memory.notes.length}/50)</h3>
        <button className="ghost" disabled={busy} onClick={() => work(() => download('/v1/assistant/memory', 'private-memory.json'))}>Export my notes</button>
      </div>
      {memory.notes.length === 0 && <p className="muted">No notes saved. Nothing from chat is added here automatically.</p>}
      {memory.notes.map(n => <article key={n.id} style={{ padding: '16px 0', borderBottom: '1px solid var(--line)' }}>
        <b>{n.title}</b><p style={{ whiteSpace: 'pre-wrap', overflowWrap: 'anywhere' }}>{n.content}</p>
        <div className="muted" style={{ fontSize: 12 }}>Expires {new Date(n.expires_at).toLocaleString()}</div>
        {remove === n.id ? <div style={{ display: 'flex', gap: 8, flexWrap: 'wrap', marginTop: 10 }}><span>Delete this saved note?</span><button disabled={busy} onClick={() => work(async () => { await api(`/v1/assistant/memory/${encodeURIComponent(n.id)}`, { method: 'DELETE' }); setRemove(null); })}>Delete note</button><button className="ghost" disabled={busy} onClick={() => setRemove(null)}>Keep it</button></div> : <button className="ghost" disabled={busy} style={{ marginTop: 10 }} onClick={() => setRemove(n.id)}>Delete...</button>}
      </article>)}
      <p className="muted" style={{ fontSize: 12 }}>Expired notes are excluded and removed on your next memory access. Old backups may retain deleted or expired notes. A role change may stop retrieval of old-role notes, but you can still export or delete them here.</p>
    </>}
  </section>;
}
