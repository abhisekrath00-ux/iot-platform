// A conservative tab-session lock, not server idempotency or proof of failure.
// Never persist the bearer token, request bodies, keys or fingerprint material.
export interface AttemptJournal { key: string; token: string; ids: Set<string>; }
const prefix = 'hexthings.setup-attempts.v1.';
function read(key: string): Set<string> {
 const value = sessionStorage.getItem(key);
 if (value === null) return new Set();
 const data: unknown = JSON.parse(value);
 if (!data || typeof data !== 'object' || !('v' in data) || data.v !== 1 || !('ids' in data) || !Array.isArray(data.ids) || data.ids.length > 10000 || !data.ids.every(id=>typeof id==='string' && id.length>0 && id.length<=512)) throw new Error('Invalid attempt journal');
 return new Set(data.ids);
}
export async function loadAttemptJournal(kind: 'source'|'pairing'|'apply'): Promise<AttemptJournal> {
 const token = localStorage.getItem('iot.token');
 if (!token || !globalThis.crypto?.subtle) throw new Error('Session lock unavailable');
 const bytes = await crypto.subtle.digest('SHA-256', new TextEncoder().encode(token));
 if (localStorage.getItem('iot.token') !== token) throw new Error('Session changed');
 const hash = Array.from(new Uint8Array(bytes), b=>b.toString(16).padStart(2,'0')).join('');
 const key = prefix + kind + '.' + hash;
 return {key,token,ids:read(key)};
}
// Call synchronously immediately before api(). Failure never authorizes a POST.
export function recordAttempt(journal: AttemptJournal, id: string): void {
 if (!id || id.length>512 || localStorage.getItem('iot.token')!==journal.token) throw new Error('Session changed');
 const ids = read(journal.key);
 for (const prior of journal.ids) ids.add(prior);
 if (ids.has(id)) throw new Error('Attempt already recorded');
 if (ids.size>=10000) throw new Error('Attempt journal full');
 ids.add(id);
 const value=JSON.stringify({v:1,ids:[...ids]});
 sessionStorage.setItem(journal.key,value);
 if (sessionStorage.getItem(journal.key)!==value || localStorage.getItem('iot.token')!==journal.token) throw new Error('Attempt journal was not retained');
 journal.ids=ids;
}
// Only an explicit successful source response with matched gateway/session may
// release its tab lock. A GET row is never sufficient. Pairing locks stay kept.
export function releaseConfirmedSource(journal: AttemptJournal, id: string): void {
 if (!journal.key.startsWith(prefix+'source.') || localStorage.getItem('iot.token')!==journal.token) throw new Error('Source session changed');
 const ids=read(journal.key);for(const prior of journal.ids)ids.add(prior);ids.delete(id);
 const value=JSON.stringify({v:1,ids:[...ids]});sessionStorage.setItem(journal.key,value);
 if(sessionStorage.getItem(journal.key)!==value || localStorage.getItem('iot.token')!==journal.token)throw new Error('Source confirmation lock update failed');
 journal.ids=ids;
}

// Only a validated direct Apply POST receipt can release this gateway's lock.
// GET observations and a missing/expired row never prove the attempted POST failed.
export function releaseAcknowledgedApply(journal: AttemptJournal, id: string): void {
 if (!journal.key.startsWith(prefix+'apply.') || localStorage.getItem('iot.token')!==journal.token) throw new Error('Apply session changed');
 const ids=read(journal.key);for(const prior of journal.ids)ids.add(prior);ids.delete(id);
 const value=JSON.stringify({v:1,ids:[...ids]});sessionStorage.setItem(journal.key,value);
 if(sessionStorage.getItem(journal.key)!==value || localStorage.getItem('iot.token')!==journal.token)throw new Error('Apply receipt lock update failed');
 journal.ids=ids;
}
