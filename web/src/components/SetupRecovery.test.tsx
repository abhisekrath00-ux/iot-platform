import {webcrypto,createHash} from 'node:crypto';
// @vitest-environment jsdom
import {act} from 'react';
import {createRoot,Root} from 'react-dom/client';
import {beforeEach,afterEach,expect,test,vi} from 'vitest';
import SetupRecovery from './SetupRecovery';
import {api} from '../lib/api';
vi.mock('../lib/api',()=>({api:vi.fn()}));
let box:HTMLDivElement,root:Root;
const configured={session_id:'saved-999',gateway_id:'gw-999',serial:'999',gateway_status:'active',device_id:'meter'};
const findButton=(text:string)=>Array.from(box.querySelectorAll('button')).find(b=>b.textContent===text)!;
const posts=()=>vi.mocked(api).mock.calls.filter(([,init])=>init?.method==='POST');
beforeEach(()=>{vi.stubGlobal('crypto',{randomUUID:()=>webcrypto.randomUUID(),subtle:{digest:async(_algorithm:string,bytes:Uint8Array)=>new Uint8Array(createHash('sha256').update(bytes).digest()).buffer}});sessionStorage.clear();localStorage.setItem('iot.token','synthetic-session');(globalThis as any).IS_REACT_ACT_ENVIRONMENT=true;box=document.createElement('div');root=createRoot(box);});
afterEach(async()=>{await act(async()=>root.unmount());vi.resetAllMocks();});
test('saved existing gateway is recoverable with metadata only and no new claim request',async()=>{
 vi.mocked(api).mockResolvedValue([{...configured,device_id:null}]);
 await act(async()=>root.render(<SetupRecovery/>));
 expect(box.textContent).toContain('Gateway 999');expect(box.textContent).toContain('Paired identity saved');expect(box.textContent).toContain('No source assigned');
 expect(box.querySelector('a')?.getAttribute('href')).toBe('/onboarding?session=saved-999');
 expect(box.textContent).toContain('not that readings are arriving');expect(api).toHaveBeenCalledTimes(1);expect(api).toHaveBeenCalledWith('/v1/commissioning/sessions');
});
test('failed recovery is actionable and never starts replacement enrollment',async()=>{
 vi.mocked(api).mockRejectedValue(Error('offline'));await act(async()=>root.render(<SetupRecovery/>));
 expect(box.querySelector('[role=alert]')?.textContent).toContain('Do not create another gateway');expect(box.querySelector('a')).toBeNull();
 vi.mocked(api).mockResolvedValue([]);await act(async()=>findButton('Try loading again').click());expect(box.textContent).toContain('No saved setup found');expect(api).toHaveBeenCalledTimes(2);
});
test('manual refresh is GET only, hides stale actions while loading, and exposes recovered setup',async()=>{
 vi.mocked(api).mockResolvedValueOnce([configured]);await act(async()=>root.render(<SetupRecovery/>));
 let finish!:(value:unknown)=>void;
 vi.mocked(api).mockImplementationOnce(()=>new Promise(resolve=>{finish=resolve;}));
 await act(async()=>findButton('Reload saved setups').click());
 expect(box.textContent).toContain('Loading saved setups');expect(box.querySelector('a')).toBeNull();expect(box.querySelector('button')).toBeNull();
 await act(async()=>finish([{...configured,session_id:'new-session',device_id:null},configured]));
 expect(box.querySelector('a')?.getAttribute('href')).toBe('/onboarding?session=new-session');
 expect(api).toHaveBeenLastCalledWith('/v1/commissioning/sessions');expect(posts()).toHaveLength(0);
});
test('lost POST response is not resent on double click, successful refresh, missing result, or refresh failure',async()=>{
 vi.mocked(api).mockResolvedValueOnce([configured,{...configured,session_id:'older'}]);await act(async()=>root.render(<SetupRecovery/>));
 const oldButtons=Array.from(box.querySelectorAll('button')).filter(b=>b.textContent==='Add another source');
 vi.mocked(api).mockRejectedValueOnce(Error('response lost'));
 await act(async()=>{oldButtons[0].click();oldButtons[1].click();});
 expect(posts()).toHaveLength(1);expect(posts()[0][0]).toBe('/v1/gateways/gw-999/source-setup');
 expect(posts()[0][1]?.headers).toEqual({'Idempotency-Key':expect.any(String)});
 expect(box.textContent).toContain('does not resend the request');expect(box.textContent).not.toContain('response lost');
 let blocked=Array.from(box.querySelectorAll('button')).filter(b=>b.textContent==='Source setup outcome unknown');
 expect(blocked).toHaveLength(2);expect(blocked.every(b=>b.disabled)).toBe(true);
 // The new saved row is observed, never auto-opened or treated as proof which request made it.
 vi.mocked(api).mockResolvedValueOnce([{...configured,session_id:'recovered',device_id:null},configured]);
 await act(async()=>findButton('Reload saved setups').click());
 expect(box.querySelector('a')?.getAttribute('href')).toBe('/onboarding?session=recovered');
 expect(box.textContent).toContain('outcome unknown');expect(posts()).toHaveLength(1);
 vi.mocked(api).mockRejectedValueOnce(Error('load failed'));
 await act(async()=>findButton('Reload saved setups').click());
 expect(box.textContent).toContain('Could not confirm whether source setup was started');expect(box.textContent).toContain('Saved setup could not be loaded');
 vi.mocked(api).mockResolvedValueOnce([configured]);
 await act(async()=>findButton('Try loading again').click());
 blocked=Array.from(box.querySelectorAll('button')).filter(b=>b.textContent==='Source setup outcome unknown');expect(blocked).toHaveLength(1);expect(blocked[0].disabled).toBe(true);
 await act(async()=>oldButtons[0].click());expect(posts()).toHaveLength(1);
 expect(box.textContent).toContain('does not prove that an earlier request failed');
});
test('concurrent gateways keep independent opening and uncertain labels with one request each',async()=>{
 const other={...configured,gateway_id:'gw-222',session_id:'saved-222',serial:'222'};
 vi.mocked(api).mockResolvedValueOnce([configured,other]);await act(async()=>root.render(<SetupRecovery/>));
 let rejectFirst!:(reason:Error)=>void, rejectOther!:(reason:Error)=>void;
 vi.mocked(api).mockImplementationOnce(()=>new Promise((_resolve,reject)=>{rejectFirst=reject;})).mockImplementationOnce(()=>new Promise((_resolve,reject)=>{rejectOther=reject;}));
 const buttons=Array.from(box.querySelectorAll('button')).filter(b=>b.textContent==='Add another source');
 await act(async()=>{buttons[0].click();buttons[1].click();buttons[0].click();});
 expect(posts()).toHaveLength(2);expect(box.textContent?.match(/Opening source setup/g)).toHaveLength(2);
 await act(async()=>rejectFirst(Error('uncertain')));
 expect(box.textContent).toContain('Source setup outcome unknown');expect(box.textContent).toContain('Opening source setup...');
 await act(async()=>rejectOther(Error('uncertain')));
 expect(box.textContent).not.toContain('Opening source setup...');expect(box.textContent?.match(/Source setup outcome unknown/g)).toHaveLength(2);
 expect(posts()).toHaveLength(2);
});
test('tab-session lock survives component remount, keeps observed rows GET-only',async()=>{
 vi.mocked(api).mockResolvedValueOnce([configured]);await act(async()=>root.render(<SetupRecovery/>));
 vi.mocked(api).mockRejectedValueOnce(Error('lost'));await act(async()=>findButton('Add another source').click());
 await act(async()=>root.unmount());root=createRoot(box);vi.mocked(api).mockResolvedValueOnce([configured]);await act(async()=>root.render(<SetupRecovery/>));
 expect(findButton('Source setup outcome unknown').disabled).toBe(true);expect(box.textContent).toContain('through reloads in this tab');await act(async()=>findButton('Source setup outcome unknown').click());expect(posts()).toHaveLength(1);
});
test('storage failure or changed sign-in blocks source POST, permits read-only inspection',async()=>{
 vi.mocked(api).mockResolvedValueOnce([configured]);await act(async()=>root.render(<SetupRecovery/>));
 const write=vi.spyOn(Storage.prototype,'setItem').mockImplementation(()=>{throw Error('quota');});await act(async()=>findButton('Add another source').click());write.mockRestore();
 expect(posts()).toHaveLength(0);expect(box.textContent).toContain('could not safely retain');expect(findButton('Add another source').disabled).toBe(true);
 vi.mocked(api).mockResolvedValueOnce([configured]);await act(async()=>findButton('Reload saved setups').click());expect(posts()).toHaveLength(0);expect(findButton('Add another source').disabled).toBe(true);
});
test('corrupt stored source history blocks writes without discarding GET results',async()=>{
 sessionStorage.setItem('hexthings.setup-attempts.v1.source.'+createHash('sha256').update('synthetic-session').digest('hex'),'{');
 vi.mocked(api).mockResolvedValueOnce([configured]);await act(async()=>root.render(<SetupRecovery/>));expect(box.querySelector('a')).not.toBeNull();expect(box.textContent).toContain('could not safely retain');expect(findButton('Add another source').disabled).toBe(true);expect(posts()).toHaveLength(0);
});
test('matched confirmed source response retains explicit continue link and does not label navigation failure uncertain',async()=>{
 vi.mocked(api).mockResolvedValueOnce([configured]);await act(async()=>root.render(<SetupRecovery/>));
 vi.mocked(api).mockResolvedValueOnce({gateway_id:configured.gateway_id,session_id:'11111111-2222-4333-8444-555555555555'});await act(async()=>findButton('Add another source').click());
 expect(posts()).toHaveLength(1);expect(box.textContent).toContain('Continue the confirmed source setup');expect(box.textContent).not.toContain('Could not confirm whether');expect(findButton('Source setup started').disabled).toBe(true);
 const key=Array.from({length:sessionStorage.length},(_,i)=>sessionStorage.key(i)!).find(k=>k.includes('.source.'))!;expect(JSON.parse(sessionStorage.getItem(key)!).ids).toEqual([]);
 await act(async()=>root.unmount());root=createRoot(box);vi.mocked(api).mockResolvedValueOnce([configured]);await act(async()=>root.render(<SetupRecovery/>));expect(findButton('Add another source').disabled).toBe(false);expect(posts()).toHaveLength(1);
});
test('mismatched source receipt keeps lock across remount, never treats successful HTTP alone as source confirmation',async()=>{
 vi.mocked(api).mockResolvedValueOnce([configured]);await act(async()=>root.render(<SetupRecovery/>));vi.mocked(api).mockResolvedValueOnce({gateway_id:'other-gateway',session_id:'11111111-2222-4333-8444-555555555555'});await act(async()=>findButton('Add another source').click());expect(box.textContent).toContain('outcome unknown');await act(async()=>root.unmount());root=createRoot(box);vi.mocked(api).mockResolvedValueOnce([configured]);await act(async()=>root.render(<SetupRecovery/>));expect(findButton('Source setup outcome unknown').disabled).toBe(true);expect(posts()).toHaveLength(1);
});
test('source POST sees the retained lock before the request can start',async()=>{
 vi.mocked(api).mockResolvedValueOnce([configured]);await act(async()=>root.render(<SetupRecovery/>));let recorded=false;vi.mocked(api).mockImplementationOnce(()=>{const key=sessionStorage.key(0);recorded=!!key&&JSON.parse(sessionStorage.getItem(key)!).ids.includes(configured.gateway_id);return Promise.reject(Error('lost'));});await act(async()=>findButton('Add another source').click());expect(recorded).toBe(true);expect(posts()).toHaveLength(1);
});
