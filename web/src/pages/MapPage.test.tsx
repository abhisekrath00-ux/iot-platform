// @vitest-environment jsdom
import {act} from 'react';
import {createRoot} from 'react-dom/client';
import {afterEach,expect,test,vi} from 'vitest';
import MapPage from './MapPage';
import {api} from '../lib/api';
import {downloadTrailCsv} from '../lib/trailCsv';
vi.mock('../lib/trailCsv',()=>({downloadTrailCsv:vi.fn()}));
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

test('CSV export uses loaded displayed rows and disables on empty/loading/failure',async()=>{
 box=document.createElement('div');document.body.append(box);root=createRoot(box);
 const points=[{lat:-1,lon:2,at:'2026-10-24T00:00:00Z',source:'manual'}];
 let resolve!:(x:any)=>void;
 vi.mocked(api).mockImplementation((url:string)=>{
  if(url==='/v1/map')return Promise.resolve({devices:[],geofences:[],note:''}) as any;
  if(url==='/v1/devices')return Promise.resolve([{id:'demo',name:'Demo'}]) as any;
  if(url.includes('/track'))return new Promise(r=>{resolve=r}) as any;
  return Promise.resolve({tiles:false,attribution:''}) as any;
 });
 await act(async()=>root.render(<MapPage/>));
 const sel=box.querySelector('#mt-device') as HTMLSelectElement;
 await act(async()=>{Object.getOwnPropertyDescriptor(HTMLSelectElement.prototype,'value')!.set!.call(sel,'demo');sel.dispatchEvent(new Event('change',{bubbles:true}))});
 const button=()=>[...box.querySelectorAll('button')].find(b=>b.textContent==='Export shown CSV')!;
 expect(button().disabled).toBe(true);
 await act(async()=>resolve({points,total:1000}));expect(button().disabled).toBe(false);
 await act(async()=>button().click());expect(downloadTrailCsv).toHaveBeenCalledWith('demo',24,1000,points);
 await act(async()=>{[...box.querySelectorAll('button')].find(b=>b.textContent==='Refresh')!.click()});expect(button().disabled).toBe(true);
 await act(async()=>resolve({points:[],total:0}));expect(button().disabled).toBe(true);
});

test('source catalog errors block saving, retry validates points and ignores prior device response',async()=>{
 box=document.createElement('div');document.body.append(box);root=createRoot(box);
 const waits:{resolve:(v:unknown)=>void;reject:(e:Error)=>void}[]=[];
 vi.mocked(api).mockImplementation((url:string)=>{
  if(url==='/v1/map')return Promise.resolve({devices:[],geofences:[],note:''}) as any;
  if(url==='/v1/devices')return Promise.resolve([{id:'a',name:'A',lat_point:'lat',lon_point:'lon'},{id:'b',name:'B',lat_point:'north',lon_point:'east'}]) as any;
  if(url==='/v1/points')return new Promise((resolve,reject)=>waits.push({resolve,reject})) as any;
  return Promise.resolve({tiles:false,attribution:''}) as any;
 });
 await act(async()=>root.render(<MapPage/>));
 const select=async(id:string,value:string)=>act(async()=>{const s=box.querySelector(id)!;Object.getOwnPropertyDescriptor(HTMLSelectElement.prototype,'value')!.set!.call(s,value);s.dispatchEvent(new Event('change',{bubbles:true}))});
 const save=()=>[...box.querySelectorAll('button')].find(b=>b.textContent==='Save source')!;
 await select('#ms-dev','a');expect(save().disabled).toBe(true);
 await select('#ms-dev','b');await act(async()=>waits[1].reject(Error('422: complete catalog unavailable')));
 expect(box.textContent).toContain('Source points unavailable');expect(save().disabled).toBe(true);
 await act(async()=>waits[0].resolve([{device_id:'a',point_id:'lat'},{device_id:'a',point_id:'lon'}]));
 expect((box.querySelector('#ms-lat') as HTMLSelectElement).options.length).toBe(1);
 await act(async()=>[...box.querySelectorAll('button')].find(b=>b.textContent==='Reload source points')!.click());
 await act(async()=>waits[2].resolve([{device_id:'b',point_id:'north'},{device_id:'b',point_id:'east'}]));
 expect(save().disabled).toBe(false);expect((box.querySelector('#ms-lat') as HTMLSelectElement).value).toBe('north');
 await act(async()=>[...box.querySelectorAll('button')].find(b=>b.textContent==='Reload source points')!.click());expect(save().disabled).toBe(true);
 await act(async()=>waits[3].resolve({bad:true}));expect(box.textContent).toContain('Incomplete point catalog');expect(save().disabled).toBe(true);
 expect(vi.mocked(api).mock.calls.filter(c=>c[1]?.method)).toHaveLength(0);
});
