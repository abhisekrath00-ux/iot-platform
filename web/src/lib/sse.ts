// Reads a server-sent-events body (fetch streams, not EventSource, because the request needs the
// bearer token and a POST body). Calls onEvent for each complete event.
export function parseSSE(buf: string): { events: { event: string; data: string }[]; rest: string } {
  const events: { event: string; data: string }[] = [];
  const parts = buf.replace(/\r\n/g, '\n').split('\n\n');
  const rest = parts.pop() ?? '';
  for (const p of parts) {
    let event = 'message'; const data: string[] = [];
    for (const line of p.split('\n')) {
      if (line.startsWith('event:')) event = line.slice(6).trim();
      else if (line.startsWith('data:')) data.push(line.slice(5).replace(/^ /, ''));
    }
    if (data.length) events.push({ event, data: data.join('\n') });
  }
  return { events, rest };
}

export async function readSSE(res: Response, onEvent: (event: string, data: unknown) => void): Promise<void> {
  if (!res.body) throw new Error('no response body');
  const rd = res.body.getReader(); const dec = new TextDecoder(); let buf = '';
  for (;;) {
    const { value, done } = await rd.read();
    if (done) break;
    buf += dec.decode(value, { stream: true });
    const { events, rest } = parseSSE(buf);
    buf = rest;
    for (const e of events) { let d: unknown = e.data; try { d = JSON.parse(e.data); } catch { /* keep text */ } onEvent(e.event, d); }
  }
}
