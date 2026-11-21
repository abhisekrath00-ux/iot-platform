// @vitest-environment jsdom
import {webcrypto} from 'node:crypto';
import {beforeEach,afterEach,expect,test,vi} from 'vitest';
import {loadAttemptJournal,recordAttempt,releaseConfirmedSource} from './attemptJournal';
beforeEach(()=>{vi.stubGlobal('crypto',webcrypto);localStorage.clear();sessionStorage.clear();localStorage.setItem('iot.token','synthetic-session-a');});
afterEach(()=>{vi.restoreAllMocks();vi.unstubAllGlobals();});
test('persist before effect, restore same session, no bearer or payload persisted, opposite decision shares lock',async()=>{
 const first=await loadAttemptJournal('pairing');recordAttempt(first,'request-1');
 const saved=sessionStorage.getItem(first.key)!;expect(saved).toBe(JSON.stringify({v:1,ids:['request-1']}));expect(first.key+saved).not.toContain('synthetic-session-a');
 const restored=await loadAttemptJournal('pairing');expect([...restored.ids]).toEqual(['request-1']);expect(()=>recordAttempt(restored,'request-1')).toThrow();
 const source=await loadAttemptJournal('source');expect(source.ids.size).toBe(0);recordAttempt(source,'request-1');
});
test('session separation, no missing credential or unsupported crypto fallback',async()=>{
 const first=await loadAttemptJournal('source');recordAttempt(first,'gateway');
 localStorage.setItem('iot.token','synthetic-session-b');const next=await loadAttemptJournal('source');expect(next.key).not.toBe(first.key);expect(next.ids.size).toBe(0);expect(()=>recordAttempt(first,'other')).toThrow();
 localStorage.removeItem('iot.token');await expect(loadAttemptJournal('source')).rejects.toThrow();
 localStorage.setItem('iot.token','synthetic-session-a');vi.stubGlobal('crypto',{});await expect(loadAttemptJournal('source')).rejects.toThrow();
});
test('live reread prevents stale component from overwriting or resending another attempt',async()=>{
 const a=await loadAttemptJournal('source'),b=await loadAttemptJournal('source');recordAttempt(a,'gateway-a');expect(()=>recordAttempt(b,'gateway-a')).toThrow();recordAttempt(b,'gateway-b');expect([...(await loadAttemptJournal('source')).ids]).toEqual(['gateway-a','gateway-b']);
});
test('corrupt or wrong-version journal fails closed, never treated as an empty history',async()=>{
 const j=await loadAttemptJournal('pairing');
 for(const value of ['{','null','{}','{"v":2,"ids":[]}','{"v":1,"ids":[42]}','{"v":1,"ids":[""]}']){sessionStorage.setItem(j.key,value);await expect(loadAttemptJournal('pairing')).rejects.toThrow();expect(()=>recordAttempt(j,'request')).toThrow();}
});
test('storage quota failure and silent retention failure refuse marking',async()=>{
 const j=await loadAttemptJournal('pairing');vi.spyOn(Storage.prototype,'setItem').mockImplementation(()=>{throw Error('quota');});expect(()=>recordAttempt(j,'request')).toThrow();expect(j.ids.size).toBe(0);
 vi.restoreAllMocks();vi.spyOn(Storage.prototype,'setItem').mockImplementation(()=>{});expect(()=>recordAttempt(j,'request')).toThrow();expect(j.ids.size).toBe(0);
});
test('session changing while digest is pending or storage commits is refused',async()=>{
 const actual=webcrypto.subtle.digest.bind(webcrypto.subtle);vi.stubGlobal('crypto',{subtle:{digest:async(...args:Parameters<typeof actual>)=>{const result=await actual(...args);localStorage.setItem('iot.token','changed');return result;}}});await expect(loadAttemptJournal('source')).rejects.toThrow();
 vi.stubGlobal('crypto',webcrypto);localStorage.setItem('iot.token','stable');const j=await loadAttemptJournal('source');const set=Storage.prototype.setItem;vi.spyOn(Storage.prototype,'setItem').mockImplementation(function(this:Storage,key,value){set.call(this,key,value);if(this===sessionStorage)set.call(localStorage,'iot.token','changed');});expect(()=>recordAttempt(j,'gateway')).toThrow();
});

test('only a confirmed source path can release its lock; pairing and other IDs remain kept',async()=>{
 const source=await loadAttemptJournal('source');recordAttempt(source,'a');recordAttempt(source,'b');releaseConfirmedSource(source,'a');expect([...(await loadAttemptJournal('source')).ids]).toEqual(['b']);
 const pairing=await loadAttemptJournal('pairing');recordAttempt(pairing,'a');expect(()=>releaseConfirmedSource(pairing,'a')).toThrow();expect([...(await loadAttemptJournal('pairing')).ids]).toEqual(['a']);
});
