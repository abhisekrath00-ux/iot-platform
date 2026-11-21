import {webcrypto,createHash} from 'node:crypto';
// @vitest-environment jsdom
import {act} from 'react';
import {createRoot,Root} from 'react-dom/client';
import {test,expect,vi,beforeEach,afterEach} from 'vitest';
import SecurePairing from './SecurePairing';
import {api} from '../lib/api';
vi.mock('../lib/api',()=>({api:vi.fn()}));
const request={id:'req',gateway_id:'gateway',serial:'DEMO',key_sha256:'key-hash',csr_sha256:'csr-hash',state:'pending',expires_at:'2026-12-01T12:00:00Z'};
let box:HTMLDivElement,root:Root;
const button=(text:string)=>[...box.querySelectorAll('button')].find(b=>b.textContent===text)!;
const posts=()=>vi.mocked(api).mock.calls.filter(([,init])=>init?.method==='POST');
async function load(){await act(async()=>button('Load pairing requests').click());}
async function compare(){await act(async()=>box.querySelector('input')!.click());}
beforeEach(()=>{vi.stubGlobal('crypto',{randomUUID:()=>webcrypto.randomUUID(),subtle:{digest:async(_algorithm:string,bytes:Uint8Array)=>new Uint8Array(createHash('sha256').update(bytes).digest()).buffer}});sessionStorage.clear();localStorage.setItem('iot.token','synthetic-session');(globalThis as any).IS_REACT_ACT_ENVIRONMENT=true;box=document.createElement('div');root=createRoot(box);vi.resetAllMocks();vi.mocked(api).mockImplementation((_path,init)=>init?.method==='POST'?Promise.reject(Error('unexpected repeated decision')):Promise.resolve([request]));});
afterEach(async()=>{await act(async()=>root.unmount());});
test('no automatic mutation, exact fingerprint review required for explicit certificate approval',async()=>{
 vi.mocked(api).mockResolvedValueOnce([request]).mockResolvedValue({state:'issued',broker_acl_state:'files_written_waiting_for_broker_proof'});
 await act(async()=>root.render(<SecurePairing/>));expect(api).not.toHaveBeenCalled();await load();
 const approve=button('Approve this key');expect(approve.disabled).toBe(true);expect(box.textContent).toContain('key-hash');
 await compare();await act(async()=>approve.click());expect(api).toHaveBeenCalledTimes(2);
 const call=posts()[0];expect(call[0]).toBe('/v1/edge/identity-migrations/req/approve');expect(JSON.parse(String(call[1]?.body))).toEqual({key_sha256:'key-hash',csr_sha256:'csr-hash',compared_local_fingerprint:true});
 expect(box.textContent).toContain('Issued does not mean connected');expect(box.textContent).toContain('diagnostic nonce');expect(box.textContent).toContain('No device command or meter read');
});
test('loading invalidates stale rows and comparison before GET, failure cannot leave actionable approval',async()=>{
 vi.mocked(api).mockResolvedValueOnce([request]);await act(async()=>root.render(<SecurePairing/>));await load();await compare();
 const oldApprove=button('Approve this key');expect(oldApprove.disabled).toBe(false);
 let reject!:(reason:Error)=>void;vi.mocked(api).mockImplementationOnce(()=>new Promise((_resolve,no)=>{reject=no;}));
 await act(async()=>button('Reload pairing requests').click());
 expect(box.querySelector('article')).toBeNull();expect(box.querySelector('input')).toBeNull();expect(button('Loading pairing requests...').disabled).toBe(true);
 await act(async()=>reject(Error('private connection detail')));
 expect(box.querySelector('article')).toBeNull();expect(box.textContent).toContain('No saved request can be approved');expect(box.textContent).not.toContain('private connection detail');expect(posts()).toHaveLength(0);
 vi.mocked(api).mockResolvedValueOnce([{...request,key_sha256:'new-key',csr_sha256:'new-csr'}]);await load();
 expect(box.querySelector('input')!.checked).toBe(false);expect(button('Approve this key').disabled).toBe(true);expect(box.textContent).toContain('new-key');expect(api).toHaveBeenLastCalledWith('/v1/edge/identity-migrations');
});
for(const action of ['approve','revoke'] as const){
 test('uncertain '+action+' cannot replay or switch decisions through pending, failed, missing and issued reads',async()=>{
  vi.mocked(api).mockResolvedValueOnce([request]);await act(async()=>root.render(<SecurePairing/>));await load();await compare();
  const approve=button('Approve this key'),revoke=button('Revoke pending request');
  vi.mocked(api).mockRejectedValueOnce(Error('lost decision response'));
  await act(async()=>{(action==='approve'?approve:revoke).click();approve.click();revoke.click();});
  expect(posts()).toHaveLength(1);expect(posts()[0][0]).toBe('/v1/edge/identity-migrations/req/'+action);
  expect(box.textContent).toContain('response was not confirmed');expect(box.textContent).toContain('edge service stopped');expect(box.textContent).not.toContain('lost decision response');
  expect(button('Approve this key').disabled).toBe(true);expect(button('Revoke pending request').disabled).toBe(true);expect(box.querySelector('input')!.checked).toBe(false);expect(box.querySelector('input')!.disabled).toBe(true);
  vi.mocked(api).mockResolvedValueOnce([request]);await act(async()=>button('Reload pairing requests').click());
  expect(box.textContent).toContain('Decision response unknown');expect(button('Revoke pending request').disabled).toBe(true);expect(button('Approve this key').disabled).toBe(true);
  vi.mocked(api).mockRejectedValueOnce(Error('read failed'));await act(async()=>button('Reload pairing requests').click());
  expect(box.querySelector('article')).toBeNull();expect(box.textContent).toContain('response was not confirmed');
  vi.mocked(api).mockResolvedValueOnce([]);await act(async()=>button('Reload pairing requests').click());
  expect(box.textContent).toContain('missing request is not proof');expect(box.querySelector('article')).toBeNull();
  vi.mocked(api).mockResolvedValueOnce([{...request,state:'issued'}]);await act(async()=>button('Reload pairing requests').click());
  expect(box.textContent).toContain('Status: issued');expect(box.textContent).toContain('not a confirmed response to that decision');expect(box.querySelector('input')).toBeNull();
  expect(posts()).toHaveLength(1);expect(api).toHaveBeenLastCalledWith('/v1/edge/identity-migrations');
 });
}
test('synchronous in-flight guard suppresses duplicate loads and opposite decisions before React renders',async()=>{
 let finish!:(value:unknown)=>void;vi.mocked(api).mockImplementationOnce(()=>new Promise(ok=>{finish=ok;}));
 await act(async()=>root.render(<SecurePairing/>));const first=button('Load pairing requests');
 await act(async()=>{first.click();first.click();});expect(api).toHaveBeenCalledTimes(1);
 await act(async()=>finish([request,{...request,id:'second',serial:'SECOND'}]));await compare();
 let finishDecision!:(value:unknown)=>void;vi.mocked(api).mockImplementationOnce(()=>new Promise(ok=>{finishDecision=ok;}));
 const approve=button('Approve this key'),revokes=[...box.querySelectorAll('button')].filter(b=>b.textContent==='Revoke pending request');
 await act(async()=>{approve.click();approve.click();revokes[0].click();revokes[1].click();});expect(posts()).toHaveLength(1);
 await act(async()=>finishDecision({broker_acl_state:'files_written_waiting_for_broker_proof'}));
 // A stale successful response in a later list does not silently re-arm this request.
 vi.mocked(api).mockResolvedValueOnce([request]);await act(async()=>button('Reload pairing requests').click());
 expect(button('Approve this key').disabled).toBe(true);expect(button('Revoke pending request').disabled).toBe(true);expect(posts()).toHaveLength(1);
});
for(const outcome of ['lost','success'] as const){
 test('tab-session decision lock survives remount after '+outcome+', cannot repeat or switch',async()=>{
  await act(async()=>root.render(<SecurePairing/>));await load();await compare();
  if(outcome==='lost')vi.mocked(api).mockRejectedValueOnce(Error('lost'));else vi.mocked(api).mockResolvedValueOnce({});
  await act(async()=>button('Revoke pending request').click());await act(async()=>root.unmount());root=createRoot(box);await act(async()=>root.render(<SecurePairing/>));await load();
  expect(box.textContent).toContain('through reloads in this tab');expect(box.textContent).toContain('Decision response unknown');expect(button('Approve this key').disabled).toBe(true);expect(button('Revoke pending request').disabled).toBe(true);expect(box.querySelector('input')!.disabled).toBe(true);expect(posts()).toHaveLength(1);
 });
}
test('pairing storage failure prevents the POST, preserves read-only inspection',async()=>{
 await act(async()=>root.render(<SecurePairing/>));await load();await compare();const write=vi.spyOn(Storage.prototype,'setItem').mockImplementation(()=>{throw Error('quota');});await act(async()=>button('Approve this key').click());write.mockRestore();expect(posts()).toHaveLength(0);expect(box.textContent).toContain('could not safely retain');expect(button('Revoke pending request').disabled).toBe(true);
 await act(async()=>button('Reload pairing requests').click());expect(box.querySelector('article')).not.toBeNull();expect(posts()).toHaveLength(0);expect(button('Revoke pending request').disabled).toBe(true);
});
test('changed sign-in never records or sends a pairing decision under another session',async()=>{
 await act(async()=>root.render(<SecurePairing/>));await load();await compare();localStorage.setItem('iot.token','other-session');await act(async()=>button('Approve this key').click());expect(posts()).toHaveLength(0);expect(box.textContent).toContain('could not safely retain');
});
test('pairing POST sees the retained request lock before the decision can start',async()=>{
 await act(async()=>root.render(<SecurePairing/>));await load();let recorded=false;vi.mocked(api).mockImplementationOnce(()=>{const key=sessionStorage.key(0);recorded=!!key&&JSON.parse(sessionStorage.getItem(key)!).ids.includes(request.id);return Promise.reject(Error('lost'));});await act(async()=>button('Revoke pending request').click());expect(recorded).toBe(true);expect(posts()).toHaveLength(1);
});
