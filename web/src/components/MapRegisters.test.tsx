// @vitest-environment jsdom
import {act} from 'react';import{createRoot,Root}from'react-dom/client';import{afterEach,beforeEach,expect,test,vi}from'vitest';
import MapRegisters from './MapRegisters';import{api}from'../lib/api';
vi.mock('../lib/api',()=>({api:vi.fn()}));
let root:Root,box:HTMLDivElement;const saved=vi.fn();let replyID='request-new';
beforeEach(()=>{(globalThis as any).IS_REACT_ACT_ENVIRONMENT=true;box=document.createElement('div');document.body.append(box);root=createRoot(box);replyID='request-new';vi.mocked(api).mockImplementation(async(path,init)=>{
 if(init?.method==='POST'&&path.endsWith('/port-test'))return{request_id:'request-new'};
 if(init?.method==='POST'&&path==='/v1/profiles')return{id:'mapped-profile'};
 return{port_test:{config:{request_id:'request-new'},result:{request_id:replyID,ok:true,readings:[{point_id:'r10',value:0x42c8},{point_id:'r11',value:0}],finished_at:'2026-11-14T04:00:00Z'}}};});});
afterEach(async()=>{await act(async()=>root.unmount());box.remove();vi.clearAllMocks();vi.useRealTimers()});
async function render(){await act(async()=>root.render(<MapRegisters session="session-1" gateway="gateway-1" onSaved={saved}/>))}
async function input(label:string,value:string){const el=box.querySelector<HTMLInputElement|HTMLSelectElement>(`[aria-label="${label}"]`)!;expect(el,label).toBeTruthy();await act(async()=>{Object.getOwnPropertyDescriptor(el.tagName==='SELECT'?HTMLSelectElement.prototype:HTMLInputElement.prototype,'value')!.set!.call(el,value);el.dispatchEvent(new Event(el.tagName==='SELECT'?'change':'input',{bubbles:true}))})}
function button(text:string){return Array.from(box.querySelectorAll('button')).find(b=>b.textContent===text)!}
async function click(text:string){await act(async()=>button(text).click())}
async function connection(){await input('Map transport','rtu');await input('Map serial port','COM7');await input('Map function','3');await input('Map listed address','11');await input('Map address convention','one');await input('Map count','2')}
async function label(){const check=box.querySelector<HTMLInputElement>('[aria-label="Include register 10"]')!;await act(async()=>check.click());await input('Map template name','Fixture temperature');await input('Map manual reference','Synthetic manual p2');for(const [field,value]of Object.entries({name:'Temperature',id:'temperature',unit:'C',scale:'0.1',min:'-40',max:'150',type:'f32',order:'abcd'}))await input(`Register 10 ${field}`,value);await act(async()=>box.querySelector<HTMLInputElement>('[aria-label="Map review"]')!.click())}
test('read range before profile; preserve returned addresses and raw values, map then save one reviewed template',async()=>{
 await render();expect(vi.mocked(api).mock.calls).toHaveLength(0);expect(box.querySelector('table')).toBeNull();await connection();await click('Read register range (read only)');expect(box.textContent).toContain('17096 / 0x42c8');expect(box.textContent).toContain('0 / 0x0000');
 const request=JSON.parse(vi.mocked(api).mock.calls.find(([,i])=>i?.method==='POST')![1]!.body as string);expect(request).toMatchObject({transport:'rtu',raw_range:true,func:3,register:10,count:2,type:'u16',word_order:'abcd'});
 expect(vi.mocked(api).mock.calls.every(([p])=>p!=='/v1/profiles')).toBe(true);await label();expect(box.textContent).toContain('Converted preview: 10 C');expect(button('Save and select reusable template').disabled).toBe(false);
 await act(async()=>{button('Save and select reusable template').click();button('Save and select reusable template').click()});expect(saved).toHaveBeenCalledTimes(1);const writes=vi.mocked(api).mock.calls.filter(([p])=>p==='/v1/profiles');expect(writes).toHaveLength(1);expect(JSON.parse(writes[0][1]!.body as string).points).toMatchObject([{id:'temperature',name:'Temperature',register:10,type:'f32',scale:0.1,unit:'C'}]);expect(box.textContent).toContain('Nothing has been applied');expect(vi.mocked(api).mock.calls.every(([p])=>!p.endsWith('/config-push'))).toBe(true);
});
test('changed range invalidates snapshot and cannot save it',async()=>{await render();await connection();await click('Read register range (read only)');await label();await input('Map listed address','12');expect(box.textContent).toContain('Connection or range changed');expect(button('Save and select reusable template').disabled).toBe(true)});
test('old replies never populate table and timeout sends no retry',async()=>{vi.useFakeTimers();replyID='request-old';await render();await connection();await click('Read register range (read only)');expect(box.querySelector('table')).toBeNull();await act(async()=>vi.advanceTimersByTimeAsync(22000));expect(box.textContent).toContain('No matching reply');expect(vi.mocked(api).mock.calls.filter(([,i])=>i?.method==='POST')).toHaveLength(1)});
test('uncertain send blocks duplicate read and save without guessing result',async()=>{await render();await connection();vi.mocked(api).mockRejectedValue(Error('lost response'));await click('Read register range (read only)');expect(box.textContent).toContain('Could not confirm whether');expect(button('Read register range (read only)').disabled).toBe(true);expect(box.querySelector('table')).toBeNull()});
test('TCP is explicitly selected and sends target without serial inference',async()=>{await render();await input('Map transport','tcp');await input('Map host','192.0.2.5');await input('Map function','3');await input('Map listed address','10');await input('Map address convention','zero');await input('Map count','2');await click('Read register range (read only)');expect(JSON.parse(vi.mocked(api).mock.calls.find(([,i])=>i?.method==='POST')![1]!.body as string)).toMatchObject({transport:'tcp',host:'192.0.2.5',net_port:502,register:10,count:2});expect(box.querySelector('[aria-label="Map serial port"]')).toBeNull()});

test('overlapping decoded words, duplicate IDs and incomplete 32-bit values cannot be saved',async()=>{
 await render();await connection();await click('Read register range (read only)');await label();
 await act(async()=>box.querySelector<HTMLInputElement>('[aria-label="Include register 11"]')!.click());
 for(const [field,value]of Object.entries({name:'Other',id:'temperature',unit:'C',scale:'1',min:'-40',max:'150',type:'f32',order:'abcd'}))await input(`Register 11 ${field}`,value);
 expect(box.textContent).toContain('duplicate id');expect(box.textContent).toContain('overlap');expect(box.textContent).toContain('complete returned words');expect(button('Save and select reusable template').disabled).toBe(true);
 expect(vi.mocked(api).mock.calls.filter(([p])=>p==='/v1/profiles')).toHaveLength(0);
});
test('two independent returned registers create one multi-point reusable template',async()=>{
 await render();await connection();await click('Read register range (read only)');await label();await input('Register 10 type','u16');await input('Register 10 max','65535');
 await act(async()=>box.querySelector<HTMLInputElement>('[aria-label="Include register 11"]')!.click());
 for(const [field,value]of Object.entries({name:'Second measurement',id:'measurement2',unit:'V',scale:'1',min:'-1',max:'65535',type:'u16',order:'abcd'}))await input(`Register 11 ${field}`,value);
 await act(async()=>box.querySelector<HTMLInputElement>('[aria-label="Map review"]')!.click());await click('Save and select reusable template');
 const writes=vi.mocked(api).mock.calls.filter(([p])=>p==='/v1/profiles');expect(writes).toHaveLength(1);expect(JSON.parse(writes[0][1]!.body as string).points).toMatchObject([{register:10,id:'temperature'},{register:11,id:'measurement2'}]);
});
