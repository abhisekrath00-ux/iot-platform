// First-run product tour: steps and progress logic (pure, unit tested).

export interface TourStep { to: string; title: string; body: string; }

export const TOUR_KEY = 'hexmon-tour-done';

export const TOUR_STEPS: TourStep[] = [
  { to: '/', title: 'Fleet', body: 'Live health of every gateway and device at a glance. Start here each shift.' },
  { to: '/onboarding', title: 'Add a device', body: 'Connect a meter, sensor or controller from the UI. Pick a profile, test the link, and watch the first reading arrive. No code needed.' },
  { to: '/devices', title: 'Devices', body: 'Search the fleet, tag devices, and open a digital twin with a health score that explains itself.' },
  { to: '/dashboards', title: 'Dashboards', body: 'Build boards with gauges, bars and trends. Drag to arrange, then open Wall mode for a control-room screen.' },
  { to: '/alerts', title: 'Alerts', body: 'Acknowledge, resolve and leave notes so the next shift knows what happened.' },
  { to: '/commands', title: 'Control', body: 'Commands need approval from a second person. Every action is recorded.' },
  { to: '/reports', title: 'Reports', body: 'Build a report, pick the time buckets, and schedule delivery to email or Slack.' },
  { to: '/settings', title: 'Settings', body: 'Notification channels and API keys for SCADA and BI tools.' },
];

export function nextStep(i: number, total = TOUR_STEPS.length): number | null {
  return i + 1 < total ? i + 1 : null;
}
export function prevStep(i: number): number { return Math.max(0, i - 1); }

export function tourDone(store: Pick<Storage, 'getItem'>): boolean { return store.getItem(TOUR_KEY) === '1'; }
export function markTourDone(store: Pick<Storage, 'setItem'>): void { store.setItem(TOUR_KEY, '1'); }
