import { NavLink, Route, Routes } from 'react-router-dom';
import Fleet from './pages/Fleet';
import Devices from './pages/Devices';
import DeviceDetail from './pages/DeviceDetail';
import Onboarding from './pages/Onboarding';
import Dashboards from './pages/Dashboards';
import Flows from './pages/Flows';
import Alerts from './pages/Alerts';
import Commands from './pages/Commands';
import Settings from './pages/Settings';
import Reports from './pages/Reports';
import Profiles from './pages/Profiles';

export default function App() {
  return (
    <div className="shell">
      <nav>
        <div className="brand">Hexmon IoT</div>
        <NavLink to="/" end>Fleet</NavLink>
        <NavLink to="/devices">Devices</NavLink>
        <NavLink to="/onboarding">Add device</NavLink>
        <NavLink to="/dashboards">Dashboards</NavLink>
        <NavLink to="/flows">Flows</NavLink>
        <NavLink to="/alerts">Alerts</NavLink>
        <NavLink to="/commands">Control</NavLink>
        <NavLink to="/reports">Reports</NavLink>
        <NavLink to="/profiles">Profiles</NavLink>
        <NavLink to="/settings">Settings</NavLink>
      </nav>
      <main>
        <Routes>
          <Route path="/" element={<Fleet />} />
          <Route path="/devices" element={<Devices />} />
          <Route path="/devices/:id" element={<DeviceDetail />} />
          <Route path="/onboarding" element={<Onboarding />} />
          <Route path="/dashboards" element={<Dashboards />} />
          <Route path="/flows" element={<Flows />} />
          <Route path="/alerts" element={<Alerts />} />
          <Route path="/commands" element={<Commands />} />
          <Route path="/reports" element={<Reports />} />
          <Route path="/profiles" element={<Profiles />} />
          <Route path="/settings" element={<Settings />} />
        </Routes>
      </main>
    </div>
  );
}
