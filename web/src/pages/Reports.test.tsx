// @vitest-environment jsdom
import { act } from 'react';
import { createRoot, Root } from 'react-dom/client';
import { afterEach, beforeEach, expect, test, vi } from 'vitest';
import Reports from './Reports';
import { api } from '../lib/api';
vi.mock('../lib/api', () => ({ api: vi.fn(), download: vi.fn() }));
let box: HTMLDivElement, root: Root;
let pending: { resolve: (r: unknown) => void; reject: (e: Error) => void }[];
beforeEach(() => {
  (globalThis as any).IS_REACT_ACT_ENVIRONMENT = true;
  pending = [];
  vi.mocked(api).mockReset();
  vi.mocked(api).mockImplementation(async (path: string) => {
    if (path === '/v1/reports/preview') return new Promise((resolve, reject) => pending.push({ resolve, reject }));
    if (path === '/v1/me') return { role: 'admin' };
    if (path === '/v1/reports/options') return { sites: [], assets: [], devices: [] };
    return [];
  });
  box = document.createElement('div'); document.body.append(box); root = createRoot(box);
});
afterEach(async () => { await act(async () => root.unmount()); box.remove(); });
const button = (text: string) => [...box.querySelectorAll('button')].find(b => b.textContent === text)!;
const calls = () => vi.mocked(api).mock.calls.filter(c => c[0] === '/v1/reports/preview');
async function changeName(text: string) {
  await act(async () => {
    const input = box.querySelector('input[placeholder="Weekly energy summary"]')!;
    Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')!.set!.call(input, text);
    input.dispatchEvent(new Event('input', { bubbles: true }));
  });
}
test('pending preview blocks duplicate requests and cancellation ignores a late success', async () => {
  await act(async () => root.render(<Reports />));
  await act(async () => { button('Preview').click(); button('Preview').click(); });
  expect(calls()).toHaveLength(1);
  expect(button('Building preview...').disabled).toBe(true);
  expect(box.textContent).toContain('No report is saved or sent');
  await act(async () => button('Cancel preview').click());
  await act(async () => pending[0].resolve({ html: '<h1>obsolete</h1>', rows: 2 }));
  expect(box.querySelector('iframe')).toBeNull();
  expect(button('Preview').disabled).toBe(false);
});
test('draft edits clear a preview and old results/errors cannot replace a newer snapshot', async () => {
  await act(async () => root.render(<Reports />));
  await act(async () => button('Preview').click());
  await changeName('New draft');
  await act(async () => button('Preview').click());
  expect(JSON.parse(calls()[1][1]!.body as string).name).toBe('New draft');
  await act(async () => pending[1].resolve({ html: '<h1>new</h1>', rows: 0 }));
  expect(box.querySelector('iframe')!.getAttribute('srcdoc')).toContain('new');
  expect(box.textContent).toContain('(0 rows)');
  expect(box.querySelector('iframe')!.getAttribute('sandbox')).toBe('');
  await act(async () => pending[0].reject(Error('obsolete failure')));
  expect(box.textContent).not.toContain('obsolete failure');
  expect(box.querySelector('iframe')).not.toBeNull();
  await changeName('Changed again');
  expect(box.querySelector('iframe')).toBeNull();
  expect(box.textContent).not.toContain('Snapshot of this draft');
});
test('failed or incomplete refresh clears old HTML and permits retry, with no save or delivery', async () => {
  await act(async () => root.render(<Reports />));
  await act(async () => button('Preview').click());
  await act(async () => pending[0].resolve({ html: '<h1>first</h1>', rows: 1 }));
  await act(async () => button('Preview').click());
  expect(box.querySelector('iframe')).toBeNull();
  await act(async () => pending[1].reject(Error('503: synthetic unavailable')));
  expect(box.querySelector('[role="alert"]')!.textContent).toContain('Preview failed');
  expect(box.querySelector('iframe')).toBeNull();
  await act(async () => button('Preview').click());
  await act(async () => pending[2].resolve({ html: '', rows: 0 }));
  expect(box.textContent).toContain('incomplete preview');
  expect(vi.mocked(api).mock.calls.filter(c => c[1]?.method)).toHaveLength(3);
  expect(calls()).toHaveLength(3);
});

test('edit binds version, blocks duplicate pending saves and disables draft changes', async () => {
  let finish!: (r: unknown) => void;
  const saved = { id: 'r1', name: 'Stored draft', version: 7, definition: { metrics: [], window_hours: 24, group_by: 'hour' } };
  const reads = vi.mocked(api).getMockImplementation()!;
  vi.mocked(api).mockImplementation(async (p: string, init?: RequestInit) => {
    if (p === '/v1/reports' && !init) return [saved];
    if (init?.method === 'PUT') return new Promise(resolve => { finish = resolve; });
    return reads(p, init);
  });
  window.scrollTo = vi.fn();
  await act(async () => root.render(<Reports />));
  await act(async () => button('Edit').click());
  expect(box.textContent).toContain('Edit report (version 7)');
  const form = box.querySelector('form')!;
  await act(async () => { form.dispatchEvent(new Event('submit', { bubbles: true, cancelable: true })); form.dispatchEvent(new Event('submit', { bubbles: true, cancelable: true })); });
  const writes = vi.mocked(api).mock.calls.filter(c => c[1]?.method === 'PUT');
  expect(writes).toHaveLength(1);
  expect(writes[0][1]!.headers).toEqual({ 'If-Match': '7' });
  expect(box.querySelector('fieldset')!.disabled).toBe(true);
  expect(button('Saving...').disabled).toBe(true);
  await act(async () => finish({ id: 'r1', version: 8 }));
  expect(box.textContent).toContain('Report saved');
  expect(box.textContent).not.toContain('Edit report (version');
});

test('uncertain save preserves draft and blocks blind retry; missing edit version never writes', async () => {
  const saved = { id: 'r1', name: 'Stored draft', version: 3, definition: { metrics: [], window_hours: 24, group_by: 'hour' } };
  const reads = vi.mocked(api).getMockImplementation()!;
  let missing = false;
  vi.mocked(api).mockImplementation(async (p: string, init?: RequestInit) => {
    if (p === '/v1/reports' && !init) return [{ ...saved, version: missing ? undefined : 3 }];
    if (init?.method === 'PUT') throw Error('network unavailable');
    return reads(p, init);
  });
  window.scrollTo = vi.fn();
  await act(async () => root.render(<Reports />));
  await act(async () => button('Edit').click());
  await changeName('Keep my draft');
  await act(async () => box.querySelector('form')!.dispatchEvent(new Event('submit', { bubbles: true, cancelable: true })));
  expect(box.textContent).toContain('outcome may be uncertain');
  expect((box.querySelector('input[placeholder="Weekly energy summary"]') as HTMLInputElement).value).toBe('Keep my draft');
  expect(button('Save changes (new version)').disabled).toBe(true);
  expect(button('Duplicate as draft').disabled).toBe(true);
  await act(async () => box.querySelector('form')!.dispatchEvent(new Event('submit', { bubbles: true, cancelable: true })));
  expect(vi.mocked(api).mock.calls.filter(c => c[1]?.method === 'PUT')).toHaveLength(1);
  missing = true;
  await act(async () => button('Reload reports to check').click());
  await act(async () => button('Edit').click());
  expect(box.textContent).toContain('version unavailable');
  expect(button('Save changes (new version)').disabled).toBe(true);
  await act(async () => box.querySelector('form')!.dispatchEvent(new Event('submit', { bubbles: true, cancelable: true })));
  expect(box.textContent).toContain('No version is available');
  expect(vi.mocked(api).mock.calls.filter(c => c[1]?.method === 'PUT')).toHaveLength(1);
});


test('cancel edit clears inherited delivery and report content before a new draft', async () => {
 const saved = { id: 'r1', name: 'Stored', version: 2, channel_id: 'c1', schedule_cron: '0 8 * * *', definition: { metrics: [{device_id:'d',point_id:'p'}], window_hours: 168, group_by: 'day' } };
 const reads = vi.mocked(api).getMockImplementation()!;
 vi.mocked(api).mockImplementation(async (p: string, i?: RequestInit) => p === '/v1/reports' && !i ? [saved] : p === '/v1/notifications/channels' ? [{id:'c1',type:'email',target:'synthetic@example.invalid'}] : reads(p,i));
 window.scrollTo=vi.fn();
 await act(async()=>root.render(<Reports/>));
 await act(async()=>button('Edit').click());
 await act(async()=>button('Cancel edit').click());
 await changeName('Fresh draft');
 await act(async()=>button('Preview').click());
 const draft=JSON.parse(calls()[0][1]!.body as string);
 expect(draft.definition.window_hours).toBe(24);expect(draft.definition.metrics).toEqual([]);
 const channel=[...box.querySelectorAll('select')].find(s=>[...s.options].some(o=>o.value==='c1'))!;
 expect(channel.value).toBe('');
 expect((box.querySelector('input[placeholder="custom: 0 6 * * 1"]') as HTMLInputElement).value).toBe('');
});

test('saved report loading and error never claim empty or leave old actions available', async () => {
  let finish!: (r: unknown) => void; let fail = false;
  const reads = vi.mocked(api).getMockImplementation()!;
  vi.mocked(api).mockImplementation(async (p: string, i?: RequestInit) => {
    if (p === '/v1/reports' && !i) { if (fail) throw Error('503: synthetic storage unavailable'); return new Promise(r => { finish = r; }); }
    return reads(p, i);
  });
  await act(async () => root.render(<Reports />));
  expect(box.textContent).toContain('Loading saved reports');
  expect(box.textContent).not.toContain('No reports yet.');
  expect(button('Refreshing reports...').disabled).toBe(true);
  await act(async () => finish([{id:'r',name:'Saved synthetic',version:1,definition:{metrics:[],window_hours:24,group_by:'hour'}}]));
  expect(button('Edit')).toBeTruthy(); expect(box.textContent).toContain('List received:');
  await changeName('Keep this unsaved draft');
  fail = true;
  await act(async () => button('Refresh saved reports').click());
  expect(box.textContent).toContain('Saved reports could not be loaded');
  expect(box.textContent).not.toContain('No reports yet.');
  expect(box.textContent).not.toContain('List received:');
  expect(button('Edit')).toBeUndefined(); expect(button('Run now')).toBeUndefined();
  expect((box.querySelector('input[placeholder="Weekly energy summary"]') as HTMLInputElement).value).toBe('Keep this unsaved draft');
  fail = false;
  await act(async () => button('Refresh saved reports').click());
  await act(async () => finish([]));
  expect(box.textContent).toContain('No reports yet.');
  expect(box.textContent).not.toContain('Saved reports could not be loaded');
});

test('a late earlier report list cannot replace a newer refresh and malformed lists fail closed', async () => {
  const requests: ((v: unknown) => void)[] = [];
  const saved = {id:'r',name:'Old snapshot',version:1,definition:{metrics:[],window_hours:24,group_by:'hour'}};
  const reads = vi.mocked(api).getMockImplementation()!;
  vi.mocked(api).mockImplementation(async (p: string, i?: RequestInit) => p === '/v1/reports' && !i ? new Promise(r => requests.push(r)) : reads(p,i));
  await act(async()=>root.render(<Reports/>));
  // Direct event dispatch models two refresh sources reaching the handler before render.
  const refresh = button('Refreshing reports...');
  await act(async()=>refresh.dispatchEvent(new MouseEvent('click',{bubbles:true})));
  // The disabled control must not start another request.
  expect(requests).toHaveLength(1);
  await act(async()=>requests[0]([saved]));
  const b=button('Refresh saved reports');
  await act(async()=>{b.click();b.click();});
  expect(requests).toHaveLength(3);
  await act(async()=>requests[2]([{...saved,name:'New snapshot',version:2}]));
  await act(async()=>requests[1]([saved]));
  expect(box.textContent).toContain('New snapshot');expect(box.textContent).not.toContain('Old snapshot');
  await act(async()=>button('Refresh saved reports').click());
  await act(async()=>requests[3]({reports:[]}));
  expect(box.textContent).toContain('incomplete report list');expect(button('Edit')).toBeUndefined();
});

test('searchable keyboard point palette and explicit metric/section ordering reach preview and version-bound save', async () => {
  const data={id:'r',name:'Ordering test',version:7,definition:{metrics:[{device_id:'d1',point_id:'power'},{device_id:'d2',point_id:'energy'}],window_hours:24,group_by:'hour',sections:[{title:'Method',body:'First',page_break:true},{title:'Caveat',body:'Second'}]},channel_id:null,schedule_cron:null,last_run_at:null};
  const base=vi.mocked(api).getMockImplementation()!;
  vi.mocked(api).mockImplementation(async(p,i)=>{if(p==='/v1/reports'&&!i)return[data];if(p==='/v1/points')return[{device_id:'d1',device_name:'Meter A',point_id:'power',unit:'kW'},{device_id:'d2',device_name:'Meter B',point_id:'energy',unit:'kWh'}];if(i?.method==='PUT')return{};return base(p,i)});
  await act(async()=>root.render(<Reports/>));
  const a=(label:string)=>box.querySelector<HTMLButtonElement>(`button[aria-label="${label}"]`)!;
  const search=box.querySelector('input[type="search"]')!;
  await act(async()=>{Object.getOwnPropertyDescriptor(HTMLInputElement.prototype,'value')!.set!.call(search,'Meter B');search.dispatchEvent(new Event('input',{bubbles:true}))});
  expect(a('Add Meter A / power')).toBeNull();expect(a('Add Meter B / energy').type).toBe('button');
  await act(async()=>button('Edit').click());
  expect(a('Move metric 1 up').disabled).toBe(true);expect(a('Move metric 2 down').disabled).toBe(true);
  await act(async()=>a('Move metric 2 up').click());
  await act(async()=>{const select=box.querySelector<HTMLSelectElement>('#note-position-0')!;select.value='before_data';select.dispatchEvent(new Event('change',{bubbles:true}))});
  await act(async()=>a('Move section 1 down').click());
  await act(async()=>button('Preview').click());
  const def=JSON.parse(calls()[0][1]!.body as string).definition;
  expect(def.metrics).toEqual([data.definition.metrics[1],data.definition.metrics[0]]);
  expect(def.sections.map((s:any)=>s.title)).toEqual(['Caveat','Method']);expect(def.sections[1].page_break).toBe(true);expect(def.sections[1].position).toBe('before_data');
  expect(vi.mocked(api).mock.calls.filter(c=>c[1]?.method==='PUT')).toHaveLength(0);
  await act(async()=>box.querySelector('form')!.dispatchEvent(new Event('submit',{bubbles:true,cancelable:true})));
  const save=vi.mocked(api).mock.calls.find(c=>c[1]?.method==='PUT')!;
  expect(save[1]!.headers).toEqual({'If-Match':'7'});expect(JSON.parse(save[1]!.body as string).definition).toEqual(def);
});

test('duplicate confirmation copies supported fields into a channel-less unscheduled new draft without writes',async()=>{
 const saved={id:'original',name:'Weekly source',version:12,customer_id:'customer-a',channel_id:'c1',schedule_cron:'0 9 * * *',definition:{metrics:[{device_id:'d1',point_id:'p1'}],window_hours:168,group_by:'day',chart:'bar',sections:[{title:'Intro',body:'Method',position:'before_data',page_break:true}],subreports:[{report_id:'sub1'}]}};
 const reads=vi.mocked(api).getMockImplementation()!;let savedBody:any;
 vi.mocked(api).mockImplementation(async(p,i)=>{if(p==='/v1/reports'&&!i)return[saved];if(p==='/v1/notifications/channels')return[{id:'c1',type:'email',target:'synthetic@example.invalid',enabled:true}];if(p==='/v1/points')return[{device_id:'d1',device_name:'Meter',point_id:'p1',unit:'kW'}];if(p==='/v1/reports'&&i?.method==='POST'){savedBody=JSON.parse(i.body as string);return{id:'new-id'}};return reads(p,i)});
 window.scrollTo=vi.fn();const confirm=vi.spyOn(window,'confirm').mockReturnValue(false);
 await act(async()=>root.render(<Reports/>));await changeName('Unfinished draft');await act(async()=>button('Duplicate as draft').click());expect((box.querySelector('input[placeholder="Weekly energy summary"]') as HTMLInputElement).value).toBe('Unfinished draft');
 confirm.mockReturnValue(true);await act(async()=>button('Edit').click());await act(async()=>button('Preview').click());await act(async()=>pending[0].resolve({html:'<h1>Old preview</h1>',rows:1}));expect(box.querySelector('iframe')).not.toBeNull();
 await act(async()=>button('Duplicate as draft').click());expect(confirm).toHaveBeenCalledWith(expect.stringContaining('Replace the current local draft'));expect(box.querySelector('iframe')).toBeNull();expect(box.textContent).not.toContain('Edit report (version');expect(box.textContent).toContain('Nothing is saved or sent');expect(box.textContent).toContain('subreports remain references');expect(vi.mocked(api).mock.calls.filter(c=>c[1]?.method&&c[0]!=='/v1/reports/preview')).toHaveLength(0);
 await act(async()=>button('Preview').click());const def=JSON.parse(calls()[1][1]!.body as string);expect(def.name).toBe('Copy of Weekly source');expect(def.definition.sections).toEqual(saved.definition.sections);expect(def.definition.subreports).toEqual(saved.definition.subreports);
 await act(async()=>box.querySelector('form')!.dispatchEvent(new Event('submit',{bubbles:true,cancelable:true})));
 expect(savedBody.schedule_cron).toBe('');expect(savedBody.channel_id).toBe('');expect(savedBody.customer_id).toBeUndefined();expect(savedBody.id).toBeUndefined();expect(savedBody.definition).toEqual(def.definition);expect(vi.mocked(api).mock.calls.filter(c=>c[1]?.method==='PUT')).toHaveLength(0);confirm.mockRestore();
});

test('duplicate is hidden from customer-scoped viewers and disabled during saves or uncertain outcomes',async()=>{
 let finish!:(r:unknown)=>void;let role='admin';const saved={id:'r',name:'Source',version:2,definition:{metrics:[],window_hours:24,group_by:'hour'}};const reads=vi.mocked(api).getMockImplementation()!;
 vi.mocked(api).mockImplementation(async(p,i)=>{if(p==='/v1/me')return role==='admin'?{role}:{role:'viewer',customer_id:'customer'};if(p==='/v1/reports'&&!i)return[saved];if(i?.method==='PUT')return new Promise(r=>finish=r);return reads(p,i)});
 window.scrollTo=vi.fn();await act(async()=>root.render(<Reports/>));await act(async()=>button('Edit').click());await act(async()=>box.querySelector('form')!.dispatchEvent(new Event('submit',{bubbles:true,cancelable:true})));expect(button('Duplicate as draft').disabled).toBe(true);await act(async()=>finish({}));role='scoped';await act(async()=>root.unmount());root=createRoot(box);await act(async()=>root.render(<Reports/>));expect(button('Duplicate as draft')).toBeUndefined();
});

test('point catalog failure is not empty success, retry clears stale points and preserves draft', async () => {
 const base=vi.mocked(api).getMockImplementation()!;let attempt=0;let finish!: (r:unknown)=>void;
 vi.mocked(api).mockImplementation(async(p,i)=>{if(p==='/v1/points'){attempt++;if(attempt===1)throw Error('422: complete catalog unavailable');if(attempt===2)return[{device_id:'d1',device_name:'Meter',point_id:'power',unit:'kW'}];return new Promise(resolve=>{finish=resolve})}return base(p,i)});
 await act(async()=>root.render(<Reports/>));
 expect(box.textContent).toContain('Device points unavailable');expect(box.textContent).not.toContain('No device points yet');
 await changeName('Kept draft');
 await act(async()=>button('Reload device points').click());expect(box.textContent).toContain('1 of 1 device points match');
 await act(async()=>button('Reload device points').click());expect(box.querySelector('[data-testid="palette"]')!.textContent).not.toContain('Meter');expect(button('Loading device points...').disabled).toBe(true);
 await act(async()=>finish([{device_id:'broken'}]));expect(box.textContent).toContain('incomplete point catalog');expect(box.textContent).not.toContain('No device points yet');
 expect((box.querySelector('input[placeholder="Weekly energy summary"]') as HTMLInputElement).value).toBe('Kept draft');
 expect(vi.mocked(api).mock.calls.filter(c=>c[1]?.method)).toHaveLength(0);
});
