import { NavLink, Route, Routes } from 'react-router-dom';
import { useEffect, useState } from 'react';
import { api } from './lib/api';
import { useTheme } from './lib/theme';
import Tour from './components/Tour';
import { tourDone } from './lib/tour';
import Fleet from './pages/Fleet';
import Devices from './pages/Devices';
import DeviceDetail from './pages/DeviceDetail';
import Onboarding from './pages/Onboarding';
import Scan from './pages/Scan';
import Dashboards from './pages/Dashboards';
import Flows from './pages/Flows';
import FlowEditor from './pages/FlowEditor';
import Alerts from './pages/Alerts';
import Commands from './pages/Commands';
import Settings from './pages/Settings';
import Audit from './pages/Audit';
import Reports from './pages/Reports';
import Profiles from './pages/Profiles';
import Assets from './pages/Assets';
import Assistant from './pages/Assistant';
import Kpis from './pages/Kpis';

const icons: Record<string, JSX.Element> = {
  'Fleet': <svg viewBox="0 0 24 24"><path d="M3 12h4l3-8 4 16 3-8h4"/></svg>,
  'Devices': <svg viewBox="0 0 24 24"><rect x="4" y="4" width="16" height="16" rx="3"/><path d="M9 9h6v6H9z"/></svg>,
  'Add device': <svg viewBox="0 0 24 24"><circle cx="12" cy="12" r="9"/><path d="M12 8v8M8 12h8"/></svg>,
  'Scan': <svg viewBox="0 0 24 24"><circle cx="12" cy="12" r="9"/><circle cx="12" cy="12" r="4"/><path d="M12 12l6-6"/></svg>,
  'Dashboards': <svg viewBox="0 0 24 24"><rect x="3" y="3" width="7" height="9" rx="2"/><rect x="14" y="3" width="7" height="5" rx="2"/><rect x="14" y="12" width="7" height="9" rx="2"/><rect x="3" y="16" width="7" height="5" rx="2"/></svg>,
  'Assistant': <svg viewBox="0 0 24 24"><path d="M4 5h16v11H9l-5 4z"/><path d="M8 9h8M8 12h5"/></svg>,
  'Flows': <svg viewBox="0 0 24 24"><circle cx="6" cy="6" r="2.5"/><circle cx="18" cy="18" r="2.5"/><path d="M8.5 6H14a4 4 0 014 4v5.5"/></svg>,
  'Alerts': <svg viewBox="0 0 24 24"><path d="M6 16V11a6 6 0 1112 0v5l2 2H4z"/><path d="M10 21h4"/></svg>,
  'Control': <svg viewBox="0 0 24 24"><circle cx="12" cy="12" r="3"/><path d="M12 2v3M12 19v3M2 12h3M19 12h3M5 5l2 2M17 17l2 2M19 5l-2 2M7 17l-2 2"/></svg>,
  'Reports': <svg viewBox="0 0 24 24"><path d="M6 3h9l4 4v14H6z"/><path d="M9 13h7M9 17h7M9 9h3"/></svg>,
  'KPIs': <svg viewBox="0 0 24 24"><path d="M4 19V9M10 19V5M16 19v-7M22 19H2"/></svg>,
  'Assets': <svg viewBox="0 0 24 24"><path d="M12 3v6M5 21v-6h14v6M12 9H5v6M12 9h7v6"/></svg>,
  'Profiles': <svg viewBox="0 0 24 24"><path d="M4 6h16M4 12h16M4 18h10"/></svg>,
  'Audit': <svg viewBox="0 0 24 24"><path d="M12 3l8 3v6c0 5-3.5 8-8 9-4.5-1-8-4-8-9V6z"/><path d="M9 12l2 2 4-4"/></svg>,
  'Settings': <svg viewBox="0 0 24 24"><circle cx="12" cy="12" r="3"/><path d="M19 12a7 7 0 00-.1-1.3l2-1.5-2-3.4-2.3 1a7 7 0 00-2.2-1.3L14 3h-4l-.4 2.5a7 7 0 00-2.2 1.3l-2.3-1-2 3.4 2 1.5a7 7 0 000 2.6l-2 1.5 2 3.4 2.3-1a7 7 0 002.2 1.3L10 21h4l.4-2.5a7 7 0 002.2-1.3l2.3 1 2-3.4-2-1.5c.1-.4.1-.9.1-1.3z"/></svg>,
};

const items: [string, string][] = [["/", "Fleet"], ["/devices", "Devices"], ["/assets", "Assets"], ["/kpis", "KPIs"], ["/onboarding", "Add device"], ["/scan", "Scan"], ["/dashboards", "Dashboards"], ["/assistant", "Assistant"], ["/flows", "Flows"], ["/alerts", "Alerts"], ["/commands", "Control"], ["/reports", "Reports"], ["/profiles", "Profiles"], ["/audit", "Audit"], ["/settings", "Settings"]];

export default function App() {
  const [theme, toggle] = useTheme();
  const [brand, setBrand] = useState('Hexmon IoT');
  useEffect(() => {
    if (!localStorage.getItem('iot.token')) return;
    api<{ product_name: string; accent: string }>('/v1/branding').then(b => {
      if (b.product_name) { setBrand(b.product_name); document.title = b.product_name; }
      const root = document.documentElement.style;
      if (b.accent) {
        root.setProperty('--accent', b.accent);
        root.setProperty('--accent-2', b.accent);
        root.setProperty('--accent-bg', `color-mix(in srgb, ${b.accent} 16%, transparent)`);
      }
    }).catch(() => {});
  }, []);
  const [tour, setTour] = useState(() => !tourDone(localStorage) && !!localStorage.getItem('iot.token'));
  return (
    <div className="shell">
      {tour && <Tour onClose={() => setTour(false)} />}
      <nav>
        <div className="brand"><span className="logo">{brand.slice(0, 1).toUpperCase()}</span>{brand}</div>
        {items.map(([to, label]) => (
          <NavLink key={to} to={to} end={to === '/'}>{icons[label]}{label}</NavLink>
        ))}
        <div className="spacer" />
        <button className="theme" onClick={() => setTour(true)}>Take the tour</button>
        <button className="theme" onClick={toggle}>{theme === 'dark' ? 'Light mode' : 'Dark mode'}</button>
      </nav>
      <main>
        <Routes>
          <Route path="/" element={<Fleet />} />
          <Route path="/devices" element={<Devices />} />
          <Route path="/devices/:id" element={<DeviceDetail />} />
          <Route path="/onboarding" element={<Onboarding />} />
          <Route path="/scan" element={<Scan />} />
          <Route path="/dashboards" element={<Dashboards />} />
          <Route path="/flows" element={<Flows />} />
          <Route path="/flows/editor" element={<FlowEditor />} />
          <Route path="/alerts" element={<Alerts />} />
          <Route path="/commands" element={<Commands />} />
          <Route path="/reports" element={<Reports />} />
          <Route path="/profiles" element={<Profiles />} />
          <Route path="/assets" element={<Assets />} />
          <Route path="/assistant" element={<Assistant />} />
          <Route path="/kpis" element={<Kpis />} />
          <Route path="/audit" element={<Audit />} />
          <Route path="/settings" element={<Settings />} />
        </Routes>
      </main>
    </div>
  );
}
