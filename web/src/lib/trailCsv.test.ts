import {expect,test} from 'vitest';
import {trailCsv} from './trailCsv';
test('CSV preserves displayed order, negative coordinates, scope counts and CRLF',()=>{
 const csv=trailCsv('tracker',24,1000,[{lat:-12.5,lon:-77.25,at:'2026-10-24T00:00:00Z',source:'manual'}]);
 expect(csv).toBe('device_id,window_hours,available_samples,exported_samples,recorded_at,latitude,longitude,source\r\n"tracker",24,1000,1,"2026-10-24T00:00:00Z",-12.5,-77.25,"manual"\r\n');
});
test('CSV quotes commas, quotes and newlines and neutralizes formula text',()=>{
 const csv=trailCsv('=HYPERLINK("bad")',1,1,[{lat:1,lon:2,at:'t',source:' \t@bad,"x"\nline'}]);
 expect(csv).toContain('"\'=HYPERLINK(""bad"")"');expect(csv).toContain('"\' \t@bad,""x""\nline"');
 expect(trailCsv('x',1,0,[]).split('\r\n')).toHaveLength(2);
});
