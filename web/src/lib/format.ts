/** Human-friendly number: at most 4 significant digits for small values, grouped thousands for large. */
export function formatValue(v: number): string {
  if (!Number.isFinite(v)) return '-';
  const a = Math.abs(v);
  if (a >= 1000) return v.toLocaleString('en-US', { maximumFractionDigits: 1 });
  if (a >= 100) return v.toFixed(1).replace(/\.0$/, '');
  if (a >= 1) return v.toFixed(2).replace(/0+$/, '').replace(/\.$/, '');
  if (a === 0) return '0';
  return Number(v.toPrecision(3)).toString();
}
