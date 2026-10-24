// @vitest-environment jsdom
import {act} from 'react';
import {createRoot} from 'react-dom/client';
import {expect,test,vi} from 'vitest';
import GroupSeriesWidget from './GroupSeriesWidget';
import {downloadGroupCSV} from '../lib/groupCsv';
vi.mock('../lib/groupCsv',()=>({downloadGroupCSV:vi.fn()}));
import {api} from '../lib/api';
vi.mock('../lib/api',()=>({api:vi.fn()}));
vi.mock('recharts',()=>({LineChart:({children}:any)=><div>{children}</div>,ResponsiveContainer:({children}:any)=><div>{children}</div>,Line:()=>null,XAxis:()=>null,YAxis:()=>null,Tooltip:()=>null,CartesianGrid:()=>null}));
(globalThis as any).IS_REACT_ACT_ENVIRONMENT=true;
test('group trend uses group binding and surfaces coverage and raw-only semantics',async()=>{
 vi.mocked(api).mockResolvedValue([{t:'2026-10-23T00:00:00Z',v:45,devices:2,members:3,samples:4,unit:'kW',units_mixed:false}]);
 const box=document.createElement('div'),root=createRoot(box);
 await act(async()=>root.render(<GroupSeriesWidget w={{id:'w',type:'timeseries',device_id:'',group_id:'g root',point_id:'power',agg:'sum',title:'Plant power'}}/>));
 expect(vi.mocked(api).mock.calls[0][0]).toContain('group_id=g%20root');expect(box.textContent).toContain('2-2 of 3');expect(box.textContent).toContain('Raw samples only');
 await act(async()=>root.unmount());
});
test('mixed units withhold the trend',async()=>{
 vi.mocked(api).mockResolvedValue([{t:'2026-10-23T00:00:00Z',v:null,devices:2,members:2,samples:4,unit:'',units_mixed:true}]);
 const box=document.createElement('div'),root=createRoot(box);await act(async()=>root.render(<GroupSeriesWidget w={{id:'w',type:'timeseries',device_id:'',group_id:'g',point_id:'power',title:'Power'}}/>));expect(box.textContent).toContain('Trend withheld');expect(box.querySelector('button')!.disabled).toBe(true);await act(async()=>root.unmount());
});

test('group CSV button waits, reports errors and blocks mixed-unit exports',async()=>{
 vi.mocked(api).mockResolvedValue([{t:'2026-10-23T00:00:00Z',v:45,devices:2,members:3,samples:4,unit:'kW',units_mixed:false}]);
 const box=document.createElement('div'),root=createRoot(box);await act(async()=>root.render(<GroupSeriesWidget w={{id:'csv',type:'timeseries',device_id:'',group_id:'g',point_id:'p',agg:'avg',title:'CSV'}}/>));
 let reject!:(e:Error)=>void;vi.mocked(downloadGroupCSV).mockImplementation(()=>new Promise((_,r)=>{reject=r}));
 await act(async()=>box.querySelector('button')!.click());expect(box.querySelector('button')!.disabled).toBe(true);expect(box.textContent).toContain('Downloading CSV');expect(downloadGroupCSV).toHaveBeenCalledWith('g','p','avg');
 await act(async()=>reject(Error('Mixed units in the fresh window. CSV withheld.')));expect(box.textContent).toContain('CSV withheld');expect(box.querySelector('button')!.disabled).toBe(false);expect(box.textContent).toContain('fresh 24 h snapshot');await act(async()=>root.unmount());
});
test('empty chart disables CSV and successful export returns to ready',async()=>{
 vi.mocked(api).mockResolvedValue([]);const box=document.createElement('div'),root=createRoot(box);await act(async()=>root.render(<GroupSeriesWidget w={{id:'empty',type:'timeseries',device_id:'',group_id:'g',point_id:'p',title:'Empty'}}/>));expect(box.querySelector('button')!.disabled).toBe(true);expect(box.textContent).toContain('No raw samples');
 vi.mocked(api).mockResolvedValue([{t:'2026-10-23T00:00:00Z',v:45,devices:2,members:3,samples:4,unit:'kW',units_mixed:false}]);vi.mocked(downloadGroupCSV).mockResolvedValue();await act(async()=>root.render(<GroupSeriesWidget w={{id:'ready',type:'timeseries',device_id:'',group_id:'g2',point_id:'p',title:'Ready'}}/>));await act(async()=>box.querySelector('button')!.click());expect(box.querySelector('button')!.disabled).toBe(false);expect(box.querySelector('[role="alert"]')).toBeNull();await act(async()=>root.unmount());
});
