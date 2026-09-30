import { useEffect, useState } from 'react';

type Theme = 'light' | 'dark';
const KEY = 'hexmon-theme';

function initial(): Theme {
  const s = localStorage.getItem(KEY);
  if (s === 'light' || s === 'dark') return s;
  return window.matchMedia?.('(prefers-color-scheme: dark)').matches ? 'dark' : 'light';
}

export function useTheme(): [Theme, () => void] {
  const [t, setT] = useState<Theme>(initial);
  useEffect(() => {
    document.documentElement.dataset.theme = t;
    localStorage.setItem(KEY, t);
  }, [t]);
  return [t, () => setT(t === 'dark' ? 'light' : 'dark')];
}

/** Colors for recharts props (CSS vars do not resolve in SVG attrs reliably). */
export const chart = {
  grid: 'var(--chart-grid)', axis: 'var(--chart-axis)', line: 'var(--accent)',
  tooltip: { background: 'var(--panel)', border: '1px solid var(--line)', borderRadius: 10, boxShadow: 'var(--shadow)' },
};
