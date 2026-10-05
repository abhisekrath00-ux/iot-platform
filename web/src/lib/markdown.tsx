import type { ReactNode } from 'react';

// A small, safe markdown renderer for assistant answers: paragraphs, bold, inline code, bullet and
// numbered lists, code blocks and pipe tables. It builds React elements, never HTML strings, so a
// model (or text a model copied from a device name) cannot inject markup, scripts or links.
export type Block =
  | { t: 'p'; text: string }
  | { t: 'ul' | 'ol'; items: string[] }
  | { t: 'code'; text: string }
  | { t: 'table'; head: string[]; rows: string[][] };

const cells = (l: string) => l.trim().replace(/^\|/, '').replace(/\|$/, '').split('|').map(c => c.trim());
const isSep = (l: string) => /^\s*\|?\s*:?-{2,}:?\s*(\|\s*:?-{2,}:?\s*)*\|?\s*$/.test(l);

export function parseBlocks(src: string): Block[] {
  const out: Block[] = [];
  const lines = src.replace(/\r/g, '').split('\n');
  for (let i = 0; i < lines.length;) {
    const l = lines[i];
    if (l.trim().startsWith('```')) {
      const buf: string[] = [];
      for (i++; i < lines.length && !lines[i].trim().startsWith('```'); i++) buf.push(lines[i]);
      i++;
      out.push({ t: 'code', text: buf.join('\n') });
    } else if (l.includes('|') && i + 1 < lines.length && isSep(lines[i + 1])) {
      const head = cells(l); const rows: string[][] = [];
      for (i += 2; i < lines.length && lines[i].includes('|'); i++) rows.push(cells(lines[i]));
      out.push({ t: 'table', head, rows });
    } else if (/^\s*[-*]\s+/.test(l) || /^\s*\d+[.)]\s+/.test(l)) {
      const ordered = /^\s*\d+[.)]\s+/.test(l);
      const re = ordered ? /^\s*\d+[.)]\s+/ : /^\s*[-*]\s+/;
      const items: string[] = [];
      for (; i < lines.length && re.test(lines[i]); i++) items.push(lines[i].replace(re, ''));
      out.push({ t: ordered ? 'ol' : 'ul', items });
    } else if (l.trim() === '') {
      i++;
    } else {
      const buf: string[] = [];
      for (; i < lines.length && lines[i].trim() !== '' && !lines[i].trim().startsWith('```') && !/^\s*([-*]|\d+[.)])\s+/.test(lines[i]) && !(lines[i].includes('|') && i + 1 < lines.length && isSep(lines[i + 1])); i++) buf.push(lines[i]);
      out.push({ t: 'p', text: buf.join('\n') });
    }
  }
  return out;
}

export function inline(s: string): ReactNode[] {
  const parts: ReactNode[] = [];
  const re = /(\*\*[^*]+\*\*|`[^`]+`)/g;
  let last = 0; let m: RegExpExecArray | null; let k = 0;
  while ((m = re.exec(s))) {
    if (m.index > last) parts.push(s.slice(last, m.index));
    const tok = m[0];
    parts.push(tok.startsWith('**') ? <strong key={k++}>{tok.slice(2, -2)}</strong> : <code key={k++}>{tok.slice(1, -1)}</code>);
    last = m.index + tok.length;
  }
  if (last < s.length) parts.push(s.slice(last));
  return parts;
}

export function Markdown({ text }: { text: string }) {
  return (
    <>
      {parseBlocks(text).map((b, i) => {
        switch (b.t) {
          case 'p': return <p key={i} style={{ margin: '4px 0', whiteSpace: 'pre-wrap' }}>{inline(b.text)}</p>;
          case 'ul': return <ul key={i} style={{ margin: '4px 0', paddingLeft: 20 }}>{b.items.map((x, j) => <li key={j}>{inline(x)}</li>)}</ul>;
          case 'ol': return <ol key={i} style={{ margin: '4px 0', paddingLeft: 20 }}>{b.items.map((x, j) => <li key={j}>{inline(x)}</li>)}</ol>;
          case 'code': return <pre key={i} style={{ margin: '4px 0', padding: 8, overflowX: 'auto', fontSize: 12, background: 'rgba(127,127,127,.15)', borderRadius: 6 }}>{b.text}</pre>;
          case 'table': return (
            <div key={i} style={{ overflowX: 'auto' }}>
              <table style={{ fontSize: 12 }}><thead><tr>{b.head.map((h, j) => <th key={j}>{inline(h)}</th>)}</tr></thead>
                <tbody>{b.rows.map((r, j) => <tr key={j}>{r.map((c, k) => <td key={k}>{inline(c)}</td>)}</tr>)}</tbody></table>
            </div>
          );
        }
      })}
    </>
  );
}
