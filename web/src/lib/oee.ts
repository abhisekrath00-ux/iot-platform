export interface OeeInputs { run: string; planned: string; total: string; good: string; cycle: number; }

const REF = /^[a-z0-9_-]+\.[a-z0-9_-]+$/;

/**
 * OEE = availability x performance x quality, written in the KPI expression language:
 *   availability = run / planned, performance = total x ideal cycle / run, quality = good / total.
 * Run and planned time must use the same unit as the ideal cycle time. Uses the latest reading of each point,
 * so feed it counters and timers that accumulate over the shift.
 */
export function buildOee(i: OeeInputs): string {
  for (const r of [i.run, i.planned, i.total, i.good]) if (!REF.test(r)) throw new Error(`"${r}" is not a device.point reference`);
  if (!(i.cycle > 0) || !isFinite(i.cycle)) throw new Error('ideal cycle time must be a positive number');
  const c = Number(i.cycle.toPrecision(10));
  return `({${i.run}} / {${i.planned}}) * ({${i.total}} * ${c} / {${i.run}}) * ({${i.good}} / {${i.total}})`;
}
