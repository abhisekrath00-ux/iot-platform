// @vitest-environment jsdom
import {act} from 'react';
import {createRoot} from 'react-dom/client';
import {afterEach,expect,test,vi} from 'vitest';
import MapPage from './MapPage';
import {api} from '../lib/api';
vi.mock('../lib/api',()=>({api:vi.fn()}));
(globalThis as any).IS_REACT_ACT_ENVIRONMENT=true;
let root:ReturnType<typeof createRoot>;let box:HTMLDivElement;
afterEach(async()=>{await act(async()=>root.unmount());box.remove();vi.clearAllMocks()});
test('trail selector is outside table; window, refresh, empty and failure states are explicit',async()=>{
 box=document.createElement('div');document.body.append(box);root=createRoot(box);
 let fail=false;
 vi.mocked(api).mockImplementation((url:string)=>{
  if(url==='/v1/map')return Promise.resolve({devices:[],geofences:[],note:''}) as any;
  if(url==='/v1/devices')return Promise.resolve([{id:'demo',name:'Demo tracker'}]) as any;
  if(url.includes('/track'))return fail?Promise.reject(new Error('offline')):Promise.resolve({points:[],total:0}) as any;
  return Promise.resolve({tiles:false,attribution:''}) as any;
 });
 await act(async()=>root.render(<MapPage/>));
 const sel=box.querySelector('#mt-device') as HTMLSelectElement;expect(sel.closest('table')).toBeNull();
 await act(async()=>{Object.getOwnPropertyDescriptor(HTMLSelectElement.prototype,'value')!.set!.call(sel,'demo');sel.dispatchEvent(new Event('change',{bubbles:true}))});
 expect(box.textContent).toContain('No recorded positions in this window.');
 await act(async()=>{[...box.querySelectorAll('button')].find(b=>b.textContent==='7 d')!.click()});
 expect(vi.mocked(api).mock.calls.some(c=>c[0]==='/v1/devices/demo/track?hours=168')).toBe(true);
 fail=true;await act(async()=>{[...box.querySelectorAll('button')].find(b=>b.textContent==='Refresh')!.click()});expect(box.textContent).toContain('Could not load the trail');
 await act(async()=>{[...box.querySelectorAll('button')].find(b=>b.textContent==='Hide trail')!.click()});expect(sel.value).toBe('');
});
