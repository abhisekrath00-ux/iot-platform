import { describe, it, expect } from 'vitest';
import { describeTarget } from './ControlTargets';

describe('describeTarget', () => {
  it('alarm outputs are on/off', () => expect(describeTarget({ kind: 'alarm_output' })).toBe('on / off'));
  it('ranges and sets', () => {
    expect(describeTarget({ kind: 'modbus_write', min: 10, max: 30 })).toBe('10 to 30');
    expect(describeTarget({ kind: 'modbus_write', allowed_values: [0, 50, 100] })).toBe('one of 0, 50, 100');
  });
});
