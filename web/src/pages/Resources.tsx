import { useState } from 'react';
import { Link } from 'react-router-dom';
import { GUIDES, searchGuides } from '../lib/guides';

export default function Resources() {
  const [q, setQ] = useState('');
  const [open, setOpen] = useState(GUIDES[0].id);
  const list = searchGuides(q);
  const g = list.find((x) => x.id === open) ?? list[0];
  const cats = Array.from(new Set(list.map((x) => x.category)));
  return (
    <div>
      <h2>Resources</h2>
      <p className="muted">Guides that work offline. Each one says what is built and what has not been run on real hardware.</p>
      <input aria-label="Search guides" placeholder="Search guides (e.g. modbus, localhost, rollback)" value={q} onChange={(e) => setQ(e.target.value)} style={{ maxWidth: 420 }} />
      <div style={{ display: 'grid', gridTemplateColumns: 'minmax(220px, 280px) 1fr', gap: 16, marginTop: 12 }}>
        <div>
          {cats.map((c) => (
            <div key={c} style={{ marginBottom: 12 }}>
              <div className="muted" style={{ fontSize: 12, textTransform: 'uppercase', letterSpacing: 0.5 }}>{c}</div>
              {list.filter((x) => x.category === c).map((x) => (
                <button key={x.id} style={{ display: 'block', width: '100%', textAlign: 'left', marginTop: 4, ...(x.id === g?.id ? {} : { background: 'transparent', color: 'inherit', boxShadow: 'none', border: '1px solid #cbd5e1' }) }} onClick={() => setOpen(x.id)}>{x.title}</button>
              ))}
            </div>
          ))}
          {!list.length && <p className="muted">No guide matches. Try fewer words.</p>}
        </div>
        {g && (
          <article className="card">
            <h3 style={{ marginTop: 0 }}>{g.title}</h3>
            <p className="muted">{g.summary}</p>
            {g.body.map((b, i) => 'h' in b ? <h4 key={i}>{b.h}</h4>
              : 'p' in b ? <p key={i}>{b.p}</p>
              : 'steps' in b ? <ol key={i}>{b.steps.map((s, j) => <li key={j} style={{ marginBottom: 4 }}>{s}</li>)}</ol>
              : 'note' in b ? <p key={i} className="muted" style={{ borderLeft: '3px solid #94a3b8', paddingLeft: 10 }}>{b.note}</p>
              : <p key={i}><Link to={b.link[1]}>Open {b.link[0]} →</Link></p>)}
          </article>
        )}
      </div>
    </div>
  );
}
