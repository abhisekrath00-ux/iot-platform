// @vitest-environment jsdom
import {afterEach,expect,test,vi} from 'vitest';import {downloadGroupCSV} from './groupCsv';
afterEach(()=>{vi.unstubAllGlobals();vi.restoreAllMocks();localStorage.clear()});
test('authenticated encoded download saves only CSV and revokes after dispatch',async()=>{
 localStorage.setItem('iot.token','test-token');const fetcher=vi.fn().mockResolvedValue({ok:true,headers:new Headers({'content-type':'text/csv; charset=utf-8'}),blob:async()=>new Blob(['a,b\r\n1,2\r\n'])});vi.stubGlobal('fetch',fetcher);const make=vi.fn(()=> 'blob:test');const revoke=vi.fn();vi.stubGlobal('URL',Object.assign(URL,{createObjectURL:make,revokeObjectURL:revoke}));const click=vi.spyOn(HTMLAnchorElement.prototype,'click').mockImplementation(()=>{});vi.useFakeTimers();
 await downloadGroupCSV('g /','p&','sum');expect(fetcher.mock.calls[0][0]).toContain('group_id=g+%2F&point_id=p%26');expect(fetcher.mock.calls[0][1].headers.Authorization).toBe('Bearer test-token');expect(click).toHaveBeenCalledOnce();expect(revoke).not.toHaveBeenCalled();vi.runAllTimers();expect(revoke).toHaveBeenCalledWith('blob:test');vi.useRealTimers();
});
test('server conflict and unexpected payload never save a file',async()=>{
 vi.stubGlobal('fetch',vi.fn().mockResolvedValue({ok:false,status:409}));await expect(downloadGroupCSV('g','p','sum')).rejects.toThrow('Mixed units');vi.stubGlobal('fetch',vi.fn().mockResolvedValue({ok:true,headers:new Headers({'content-type':'application/json'})}));await expect(downloadGroupCSV('g','p','sum')).rejects.toThrow('No file saved');
});
