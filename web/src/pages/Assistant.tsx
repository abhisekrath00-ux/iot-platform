import { useEffect, useRef, useState } from 'react';
import AssistantMemory from '../components/AssistantMemory';
import { Opt, WIZARDS, Wizard } from '../components/ChatWizards';
import { api } from '../lib/api';

interface Msg { role: 'user' | 'assistant'; content: string; }
interface Pending { id: string; method: string; path: string; body?: string; summary: string; status?: string; }
interface Trace { tool: string; detail: string; status: string; }
export interface Reply { reply: string; plan: string[]; trace: Trace[]; pending: Pending[]; model?: string; error?: string; fallback?: string; }

/** The API sends null for plan, trace or pending when the model produced none; the page needs arrays. */
export function normalizeReply(r: Reply): Reply {
  return { ...r, plan: r.plan ?? [], trace: r.trace ?? [], pending: r.pending ?? [] };
}

// The assistant: it plans, calls the platform as you, and reports back. Reads happen at once.
// Every change waits here for your confirmation, and nothing it proposes can approve a control command.
export default function Assistant() {
  const [msgs, setMsgs] = useState<Msg[]>([]);
  const [runs, setRuns] = useState<Reply[]>([]);
  const [text, setText] = useState('');
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState('');
  const [acts, setActs] = useState<Record<string, string>>({});
  // Deterministic guided templates: they never call the model and work with no AI configured.
  const [wiz, setWiz] = useState<Wizard | null>(null);
  const [wStep, setWStep] = useState(0);
  const [wAns, setWAns] = useState<Record<string, string>>({});
  const [wOpts, setWOpts] = useState<Opt[] | null>(null);
  const [wErr, setWErr] = useState('');
  const [wConfirm, setWConfirm] = useState(false);
  const [wBusy, setWBusy] = useState(false);
  const end = useRef<HTMLDivElement>(null);

  function startWizard(w: Wizard) {
    setWiz(w); setWStep(0); setWAns({}); setWErr(''); setWConfirm(false); setWOpts(null);
    loadStepOptions(w, 0, {});
  }
  function loadStepOptions(w: Wizard, idx: number, answers: Record<string, string>) {
    const st = w.steps[idx];
    if (st?.kind === 'select' && st.loadOptions) {
      setWOpts(null);
      st.loadOptions(answers).then(o => {
        setWOpts(o);
        if (o.length === 0) setWErr('Nothing to pick from yet. Cancel and add one first (or ask in the chat).');
      }).catch(e => { setWOpts([]); setWErr(String(e)); });
    } else { setWOpts(st?.options ?? null); }
  }
  function answerWizard(v: string) {
    if (!wiz) return;
    const st = wiz.steps[wStep];
    const bad = st.validate?.(v);
    if (bad) { setWErr(bad); return; }
    if (st.kind === 'select') {
      const pool = wOpts ?? st.options ?? [];
      const hit = pool.find(o => o.value === v || o.label === v);
      if (!hit) { setWErr('Pick one of the offered choices.'); return; }
      v = hit.value;
    }
    setWErr('');
    const next = { ...wAns, [st.key]: v };
    setWAns(next);
    if (wStep + 1 >= wiz.steps.length) { setWConfirm(true); return; }
    setWStep(wStep + 1);
    loadStepOptions(wiz, wStep + 1, next);
  }
  async function runWizard() {
    if (!wiz) return;
    setWBusy(true);
    try {
      const done = await wiz.run(wAns);
      setMsgs(ms => [...ms, { role: 'assistant', content: done }]);
      setWiz(null);
    } catch (e) { setWErr(String(e)); }
    setWBusy(false);
  }
  useEffect(() => { end.current?.scrollIntoView({ behavior: 'smooth' }); }, [runs, msgs]);

  async function send(e: React.FormEvent) {
    e.preventDefault();
    if (wiz && !wConfirm) { const t2 = text; setText(''); answerWizard(t2); return; }
    if (wiz) return; // confirmation stage: use the buttons
    const next: Msg[] = [...msgs, { role: 'user', content: text }];
    setMsgs(next); setText(''); setBusy(true); setErr('');
    try {
      const r = await api<Reply>('/v1/assistant/chat', { method: 'POST', body: JSON.stringify({ messages: next }) });
      setMsgs([...next, { role: 'assistant', content: r.reply }]);
      setRuns(rs => [...rs, normalizeReply(r)]);
    } catch (e2) {
      const m = String(e2);
      try { const j = JSON.parse(m.slice(m.indexOf('{'))); setErr(j.error + (j.fallback ? ' You can still use the question box on the Fleet page.' : '')); } catch { setErr(m); }
    }
    setBusy(false);
  }
  async function decide(id: string, what: 'confirm' | 'reject') {
    try {
      const r = await api<{ status: string; result_code?: number }>(`/v1/assistant/actions/${id}/${what}`, { method: 'POST' });
      setActs(a => ({ ...a, [id]: what === 'reject' ? 'rejected' : r.status === 'executed' ? 'done' : `failed (${r.result_code})` }));
    } catch (e) { setActs(a => ({ ...a, [id]: String(e) })); }
  }
  let ri = -1;
  return (
    <>
      <h1>Assistant</h1>
      <AssistantMemory />
      <p className="muted">Ask it to do things: "acknowledge the pump alert", "summarise this week's alerts on Line A", "draft a report of boiler temperature". It acts as you, shows its plan and steps, and waits for your confirmation before changing anything. It cannot approve control commands.</p>
      {err && <p role="alert">{err}</p>}
      <div className="card" style={{ maxWidth: 900, minHeight: 240 }}>
        {msgs.length === 0 && <p className="muted">Nothing yet.</p>}
        {msgs.map((m, i) => {
          const run = m.role === 'assistant' ? runs[++ri] : undefined;
          return (
            <div key={i} style={{ margin: '10px 0' }}>
              <div className="muted" style={{ fontSize: 12 }}>{m.role === 'user' ? 'You' : 'Assistant'}</div>
              <div style={{ whiteSpace: 'pre-wrap' }}>{m.content}</div>
              {run && run.plan.length > 0 && <ol className="muted" style={{ margin: '6px 0' }}>{run.plan.map((p, j) => <li key={j}>{p}</li>)}</ol>}
              {run && run.trace.length > 0 && (
                <details style={{ margin: '6px 0' }}>
                  <summary className="muted">{run.trace.length} step{run.trace.length === 1 ? '' : 's'} taken</summary>
                  {run.trace.map((t, j) => <div key={j} className="muted" style={{ fontSize: 12 }}><span className={`pill ${t.status === 'ok' ? 'ok' : t.status === 'proposed' ? '' : 'warn'}`}>{t.status}</span> {t.tool}: {t.detail}</div>)}
                </details>
              )}
              {run && run.pending.map(p => (
                <div key={p.id} className="card" style={{ margin: '8px 0', padding: '10px 14px' }}>
                  <b>Waiting for you:</b> {p.summary}
                  <div className="muted" style={{ fontSize: 12 }}>{p.method} {p.path}{p.body ? ` ${p.body}` : ''}</div>
                  {acts[p.id]
                    ? <span className="pill">{acts[p.id]}</span>
                    : <div style={{ marginTop: 6, display: 'flex', gap: 8 }}><button onClick={() => decide(p.id, 'confirm')}>Confirm</button><button className="ghost" onClick={() => decide(p.id, 'reject')}>Reject</button></div>}
                </div>
              ))}
            </div>
          );
        })}
        {busy && <p className="muted">Working...</p>}
        <div ref={end} />
      </div>
      {!wiz && (
        <div className="card" style={{ maxWidth: 900, marginTop: 12, padding: '10px 14px' }}>
          <span className="muted" style={{ fontSize: 12 }}>Guided templates (work without the model): </span>
          <div style={{ display: 'flex', gap: 8, flexWrap: 'wrap', marginTop: 6 }}>
            {WIZARDS.map(w => (
              <button key={w.id} type="button" className="ghost" title={w.description} onClick={() => startWizard(w)}>{w.title}</button>
            ))}
          </div>
        </div>
      )}
      {wiz && (
        <div className="card" style={{ maxWidth: 900, marginTop: 12, padding: '12px 16px' }} role="group" aria-label="Guided template">
          <b>{wiz.title}</b> <span className="muted" style={{ fontSize: 12 }}>step {Math.min(wStep + 1, wiz.steps.length)} of {wiz.steps.length} - no AI involved, you confirm before anything is written</span>
          {!wConfirm ? (
            <div style={{ marginTop: 8 }}>
              <div>{wiz.steps[wStep].prompt}</div>
              {wOpts && (
                <div style={{ display: 'flex', gap: 8, flexWrap: 'wrap', marginTop: 8 }}>
                  {wOpts.map(o => <button key={o.value} type="button" className="ghost" onClick={() => answerWizard(o.value)}>{o.label}</button>)}
                </div>
              )}
              {wOpts === null && wiz.steps[wStep].kind === 'select' && !wErr && <p className="muted">Loading choices...</p>}
              {(wiz.steps[wStep].kind !== 'select') && (
                <form onSubmit={e => { e.preventDefault(); answerWizard(text); setText(''); }} style={{ display: 'flex', gap: 8, marginTop: 8 }}>
                  <input aria-label="Your answer" value={text} onChange={e => setText(e.target.value)} autoFocus />
                  <button type="submit" disabled={!text.trim()}>Next</button>
                </form>
              )}
              {wErr && <p role="alert" style={{ marginTop: 6 }}>{wErr}</p>}
              <p style={{ marginTop: 8 }}><button type="button" className="ghost" onClick={() => setWiz(null)}>Cancel</button></p>
            </div>
          ) : (
            <div style={{ marginTop: 8 }}>
              <p>{wiz.summary(wAns)}</p>
              {wErr && <p role="alert">{wErr}</p>}
              <div style={{ display: 'flex', gap: 8 }}>
                <button type="button" disabled={wBusy} onClick={runWizard}>{wBusy ? 'Working...' : 'Confirm'}</button>
                <button type="button" className="ghost" disabled={wBusy} onClick={() => setWiz(null)}>Cancel</button>
              </div>
            </div>
          )}
        </div>
      )}
      <form onSubmit={send} style={{ display: 'flex', gap: 8, maxWidth: 900, marginTop: 12 }}>
        <input aria-label="Message the assistant" placeholder={wiz ? 'Answer above, or cancel the template' : 'What should I do?'} value={text} onChange={e => setText(e.target.value)} disabled={busy || (!!wiz && wOpts !== null && wiz.steps[wStep]?.kind === 'select')} />
        <button type="submit" disabled={busy || !!wiz || !text.trim()}>Send</button>
      </form>
      <p className="muted" style={{ fontSize: 12, maxWidth: 900 }}>Answers come from the model your admin connected and can be wrong. Check values before you confirm a change.</p>
    </>
  );
}
