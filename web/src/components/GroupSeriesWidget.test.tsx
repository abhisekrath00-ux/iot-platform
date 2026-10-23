// @vitest-environment jsdom
import {act} from 'react';
import {createRoot} from 'react-dom/client';
import {expect,test,vi} from 'vitest';
import GroupSeriesWidget from './GroupSeriesWidget';
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
 const box=document.createElement('div'),root=createRoot(box);await act(async()=>root.render(<GroupSeriesWidget w={{id:'w',type:'timeseries',device_id:'',group_id:'g',point_id:'power',title:'Power'}}/>));expect(box.textContent).toContain('Trend withheld');await act(async()=>root.unmount());
});
