import { useEffect, useLayoutEffect, useState } from 'react';
import { TOUR_STEPS, nextStep, prevStep, markTourDone } from '../lib/tour';

// Spotlight tour anchored to the sidebar links. Esc or Skip ends it and it will
// not reappear on its own; "Take the tour" in the sidebar restarts it.
export default function Tour({ onClose }: { onClose: () => void }) {
  const [i, setI] = useState(0);
  const [box, setBox] = useState<DOMRect | null>(null);
  const step = TOUR_STEPS[i];

  useLayoutEffect(() => {
    const el = document.querySelector<HTMLElement>(`nav a[href="${step.to}"]`);
    setBox(el ? el.getBoundingClientRect() : null);
  }, [i, step.to]);

  const finish = () => { markTourDone(localStorage); onClose(); };
  useEffect(() => {
    const k = (e: KeyboardEvent) => { if (e.key === 'Escape') finish(); };
    window.addEventListener('keydown', k);
    return () => window.removeEventListener('keydown', k);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  const n = nextStep(i);
  return (
    <div className="tour" role="dialog" aria-label="Product tour">
      <div className="tour-scrim" onClick={finish} />
      {box && <div className="tour-ring" style={{ top: box.top - 4, left: box.left - 4, width: box.width + 8, height: box.height + 8 }} />}
      <div className="tour-card" style={{ top: Math.max(16, (box?.top ?? 120) - 8), left: (box?.right ?? 240) + 18 }}>
        <div className="muted" style={{ fontSize: 12 }}>Step {i + 1} of {TOUR_STEPS.length}</div>
        <h3 style={{ margin: '4px 0 6px' }}>{step.title}</h3>
        <p style={{ margin: 0 }}>{step.body}</p>
        <div style={{ display: 'flex', gap: 8, marginTop: 14, justifyContent: 'flex-end' }}>
          <button className="ghost" onClick={finish}>Skip</button>
          {i > 0 && <button className="ghost" onClick={() => setI(prevStep(i))}>Back</button>}
          <button onClick={() => (n === null ? finish() : setI(n))}>{n === null ? 'Done' : 'Next'}</button>
        </div>
      </div>
    </div>
  );
}
