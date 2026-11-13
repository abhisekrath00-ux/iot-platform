// @vitest-environment jsdom
import {act} from 'react';
import {createRoot,Root} from 'react-dom/client';
import {beforeEach,afterEach,expect,test,vi} from 'vitest';
import SetupRecovery from './SetupRecovery';
import {api} from '../lib/api';
vi.mock('../lib/api',()=>({api:vi.fn()}));
let box:HTMLDivElement,root:Root;
beforeEach(()=>{(globalThis as any).IS_REACT_ACT_ENVIRONMENT=true;box=document.createElement('div');root=createRoot(box);});
afterEach(async()=>{await act(async()=>root.unmount());vi.clearAllMocks();});
test('saved existing gateway is recoverable with metadata only and no new claim request',async()=>{
 vi.mocked(api).mockResolvedValue([{session_id:'saved-999',gateway_id:'gw-999',serial:'999',gateway_status:'active',device_id:null}]);
 await act(async()=>root.render(<SetupRecovery/>));
 expect(box.textContent).toContain('Gateway 999');expect(box.textContent).toContain('Paired identity saved');expect(box.textContent).toContain('No source assigned');
 expect(box.querySelector('a')?.getAttribute('href')).toBe('/onboarding?session=saved-999');
 expect(box.textContent).toContain('not that readings are arriving');expect(api).toHaveBeenCalledTimes(1);expect(api).toHaveBeenCalledWith('/v1/commissioning/sessions');
});
test('failed recovery is actionable and never starts replacement enrollment',async()=>{
 vi.mocked(api).mockRejectedValue(Error('offline'));await act(async()=>root.render(<SetupRecovery/>));
 expect(box.querySelector('[role=alert]')?.textContent).toContain('Do not create another gateway');expect(box.querySelector('a')).toBeNull();
 vi.mocked(api).mockResolvedValue([]);await act(async()=>box.querySelector('button')!.click());expect(box.textContent).toContain('No saved setup found');expect(api).toHaveBeenCalledTimes(2);
});
