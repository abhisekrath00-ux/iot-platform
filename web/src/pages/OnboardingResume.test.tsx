// @vitest-environment jsdom
import {act} from 'react';
import {createRoot} from 'react-dom/client';
import {afterEach,expect,test,vi} from 'vitest';
import Onboarding from './Onboarding';
import {api} from '../lib/api';
vi.mock('../lib/api',()=>({api:vi.fn(),download:vi.fn(),EXTRA_DRIVERS:{},NETWORK_DRIVERS:[]}));
vi.mock('../components/EdgeDownloads',()=>({default:()=>null}));
vi.mock('../components/SiteSelect',()=>({SiteSelect:()=>null}));
afterEach(()=>{vi.clearAllMocks();window.history.replaceState(null,'','/onboarding');});
async function render(saved:unknown,fail=false){
 window.history.replaceState(null,'','/onboarding?session=session-999');
 vi.mocked(api).mockImplementation(async(path)=>{
 if(path==='/v1/sites'||path==='/v1/profiles')return [];
 if(fail)throw Error('404');return saved;
 });
 const box=document.createElement('div'),root=createRoot(box);
 await act(async()=>root.render(<Onboarding/>));return{box,root};
}
test('claimed saved gateway resumes profile assignment without minting or displaying a claim',async()=>{
 const {box,root}=await render({session_id:'session-999',gateway_id:'gw-999',serial:'999',gateway_status:'active',device_id:null,state:'claimed'});
 expect(box.textContent).toContain('Assign a device profile');expect(box.textContent).toContain('Gateway 999');expect(box.textContent).not.toContain('Create claim code');
 expect(vi.mocked(api).mock.calls.every(([,init])=>!init?.method||init.method==='GET')).toBe(true);
 await act(async()=>root.unmount());
});
test('profiled saved gateway exposes full config download without a second profile mutation',async()=>{
 const {box,root}=await render({session_id:'session-999',gateway_id:'gw-999',serial:'999',gateway_status:'active',device_id:'dev-1',state:'profiled'});
 expect(box.textContent).toContain('Download edge-agent.yaml');expect(box.textContent).not.toContain('Assign a device profile');await act(async()=>root.unmount());
});
test('failed resume blocks new claim form rather than silently starting again',async()=>{
 const {box,root}=await render(null,true);expect(box.textContent).toContain('Could not resume');expect(box.textContent).not.toContain('Create claim code');await act(async()=>root.unmount());
});
test('pending saved gateway does not invent a replacement code',async()=>{
 const {box,root}=await render({session_id:'session-999',gateway_id:'gw-999',serial:'999',gateway_status:'pending',device_id:null,state:'awaiting_claim'});
 expect(box.textContent).toContain('original code is not stored');expect(box.textContent).not.toContain('Create claim code');await act(async()=>root.unmount());
});
test('uncertain create response blocks a second claim request',async()=>{
 vi.mocked(api).mockImplementation(async(path)=>{
 if(path==='/v1/sites')return [{id:'site-1',name:'Site'}];
 if(path==='/v1/profiles')return [];
 if(path==='/v1/commissioning/sessions')return [];
 return null;
 });
 const box=document.createElement('div'),root=createRoot(box);
 await act(async()=>root.render(<Onboarding/>));
 vi.mocked(api).mockRejectedValue(Error('lost response'));
 const form=box.querySelector('form')!;
 await act(async()=>{form.dispatchEvent(new Event('submit',{bubbles:true,cancelable:true}));form.dispatchEvent(new Event('submit',{bubbles:true,cancelable:true}));});
 expect(vi.mocked(api).mock.calls.filter(([,init])=>init?.method==='POST')).toHaveLength(1);
 expect(box.textContent).toContain('Could not confirm whether');
 expect(box.textContent).toContain('No automatic retry');
 expect(box.querySelector<HTMLButtonElement>('button[type="submit"]')?.disabled).toBe(true);
 await act(async()=>root.unmount());
});
test('resumed source cannot send probe defaults without explicit review',async()=>{
 const {box,root}=await render({session_id:'session-999',gateway_id:'gw-999',serial:'999',gateway_status:'active',device_id:'dev-1',state:'profiled'});
 const send=Array.from(box.querySelectorAll<HTMLButtonElement>('button')).find(b=>b.textContent==='Send probe');
 expect(send?.disabled).toBe(true);expect(box.textContent).toContain('example test settings');
 const form=box.querySelector('form')!;
 await act(async()=>form.dispatchEvent(new Event('submit',{bubbles:true,cancelable:true})));
 expect(vi.mocked(api).mock.calls.every(([,init])=>!init?.method||init.method==='GET')).toBe(true);
 await act(async()=>root.unmount());
});
test('resumed probe restores the last authoritative request, never sends it automatically',async()=>{
 const p={port:'COM7',baud:9600,data_bits:8,stop_bits:1,parity:'even',address:17,func:3,register:3048,count:2,type:'f32',word_order:'cdab',timeout_ms:1500};
 const {box,root}=await render({session_id:'session-999',gateway_id:'gw-999',serial:'999',gateway_status:'active',device_id:'dev-1',state:'profiled',port_test:{config:p}});
 expect(box.textContent).toContain('last requested probe settings are restored');expect(box.querySelector<HTMLInputElement>('form input')?.value).toBe('COM7');
 expect(Array.from(box.querySelectorAll('select')).some(s=>s.value==='even')).toBe(true);
 expect(vi.mocked(api).mock.calls.every(([,init])=>!init?.method||init.method==='GET')).toBe(true);
 await act(async()=>root.unmount());
});
test('assigned source restores connection from saved source independently of a probe request',async()=>{
 const source={device_id:'dev-1',name:'Saved meter',driver:'modbus-generic',connection:{port:'COM8',baud:19200,data_bits:7,stop_bits:2,parity:'odd',address:21,interval_seconds:10}};
 const {box,root}=await render({session_id:'session-999',gateway_id:'gw-999',serial:'999',gateway_status:'active',device_id:'dev-1',state:'profiled',source});
 expect(box.querySelector('[aria-label="Saved source"]')?.textContent).toContain('Saved meter');expect(box.querySelector<HTMLInputElement>('form input')?.value).toBe('COM8');expect(Array.from(box.querySelectorAll('select')).some(s=>s.value==='odd')).toBe(true);
 expect(box.textContent).toContain('does not prove they are applied');
 expect(vi.mocked(api).mock.calls.every(([,init])=>!init?.method||init.method==='GET')).toBe(true);await act(async()=>root.unmount());
});
test('mismatched saved source is not silently accepted',async()=>{
 const {box,root}=await render({session_id:'session-999',gateway_id:'gw-999',serial:'999',gateway_status:'active',device_id:'dev-1',state:'profiled',source:{device_id:'foreign',name:'Wrong',driver:'modbus-generic',connection:{}}});
 expect(box.textContent).toContain('Could not resume');expect(box.textContent).not.toContain('Create claim code');await act(async()=>root.unmount());
});
test('old preview rows do not announce commissioning complete',async()=>{
 window.history.replaceState(null,'','/onboarding?session=session-999');
 vi.mocked(api).mockImplementation(async path=>{
 if(path.endsWith('/preview'))return{live:false,points:[{point_id:'power',value:12,unit:'W',quality:'measured',observed_at:'2025-01-01T00:00:00Z',recent_measured:false}]};
 if(path==='/v1/sites'||path==='/v1/profiles')return[];
 if(path==='/v1/me')return{role:'operator'};
 return{session_id:'session-999',gateway_id:'gw-999',serial:'999',gateway_status:'active',device_id:'dev-1',state:'live'};
 });
 const box=document.createElement('div'),root=createRoot(box);await act(async()=>root.render(<Onboarding/>));
 expect(box.textContent).toContain('Historical, estimated or stale');expect(box.textContent).not.toContain('Commissioning complete');expect(box.textContent).not.toContain('Recent measured data is arriving');await act(async()=>root.unmount());
});
