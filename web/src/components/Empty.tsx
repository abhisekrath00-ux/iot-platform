import { ReactNode } from 'react';

/** Friendly empty state: one line of context and an optional next action. */
export default function Empty({ title, hint, action }: { title: string; hint?: string; action?: ReactNode }) {
  return (
    <div className="card" style={{ textAlign: 'center', padding: '42px 20px' }}>
      <div style={{ fontSize: 17, fontWeight: 650, marginBottom: 6 }}>{title}</div>
      {hint && <div className="muted" style={{ maxWidth: 420, margin: '0 auto 14px' }}>{hint}</div>}
      {action}
    </div>
  );
}
