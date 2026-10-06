// In-app guides. Plain data so they work offline and can be searched and tested.
// Keep them honest: say what is built, and what has not been run on real hardware.

export type Block = { h: string } | { p: string } | { steps: string[] } | { note: string } | { link: [string, string] };
export interface Guide { id: string; title: string; category: 'Start here' | 'Devices' | 'Operate' | 'Security' | 'Reference'; summary: string; body: Block[]; }

export const GUIDES: Guide[] = [
  {
    id: 'first-day', title: 'Your first day with HexThings', category: 'Start here',
    summary: 'Sign in, add a site, add a device, see data, set an alert.',
    body: [
      { p: 'HexThings collects readings from sensors and meters, keeps them in your own database, and alerts you when something looks wrong. It runs on your own server and works without internet.' },
      { steps: ['Create a site (a plant, building or room) under Add device.', 'Add a device. For a network or serial sensor read through an edge box, use Edge setup.', 'Open Devices and click the device to see its latest values and history.', 'Build a dashboard from widgets, or use Explorer for a quick chart.', 'Open Alerts to see what fired, and acknowledge or comment.'] },
      { link: ['Add a device', '/onboarding'] }, { link: ['Edge setup', '/edge-setup'] },
      { note: 'The guided tour (Take the tour in the sidebar) walks through the main pages in under two minutes.' },
    ],
  },
  {
    id: 'edge-device', title: 'Connect a sensor through an edge box', category: 'Devices',
    summary: 'The path from "I have a meter" to "I see its data".',
    body: [
      { p: 'An edge box is a small computer (Raspberry Pi class or a Windows or Linux PC) next to your sensors. It reads them and sends the readings to the server. If the network drops it keeps readings and sends them later.' },
      { steps: ['Open Edge setup. Pick the site, name the box, choose the sensor template and the connection (RS-485 serial or network).', 'Press Create claim code. Copy it now; it is shown once.', 'Download the edge package for the box and verify its SHA256SUMS file.', 'On the box run the install command shown on the page. It checks the server address, clock and permissions, then installs the service.', 'Plug the sensor in. Within a minute the box shows as active and values appear on Devices.'] },
      { note: 'Honest status: the whole path has been tested in pieces (installer logic, claim, templates, rollouts) and never end to end on a real box and meter.' },
      { link: ['Edge setup', '/edge-setup'] },
    ],
  },
  {
    id: 'modbus-maps', title: 'How Modbus data gets names and units', category: 'Devices',
    summary: 'Raw registers are just numbers. A register map says what they mean.',
    body: [
      { p: 'A Modbus device only answers "what is in register 4". It does not send names or units. A register map (a sensor profile) records, for each register: a name, the register number, the function code, the data type, the word order, a scale, a unit, and a valid range.' },
      { h: 'Where to get a map' },
      { steps: ['Use a built-in template (Selec MX300 and MFM383A are included).', 'Import a CSV or JSON file with the columns id, register, func, type, word_order, scale, unit, min, max.', 'Or type the points in by hand from the device manual (look for the "Modbus register map" table).'] },
      { p: 'Imports are checked before you can save: unique names, no overlapping registers, valid types and ranges. Nothing is saved until you press Create profile.' },
      { note: 'Auto-detect only ever suggests. It never names or saves anything for you.' },
      { link: ['Profiles', '/profiles'] },
    ],
  },
  {
    id: 'server-address', title: 'Choosing the server address for edge boxes', category: 'Devices',
    summary: 'Why localhost does not work and how to set addresses per site.',
    body: [
      { p: 'An edge box must reach the server over the network. "localhost" only means "this machine", so an install command that uses it fails on every other computer.' },
      { steps: ['Go to Settings, Server addresses.', 'Add the address your boxes can reach, for example https://hub.example.com or http://192.168.10.5:8000. Pick it from the suggestions if it appears.', 'For a site on another network or behind NAT, add an address for that site only.', 'Add a second address with a higher priority number as a fallback.', 'Press Test. It checks from the server. Only the edge box can prove it reaches the address.'] },
      { link: ['Settings', '/settings'] },
    ],
  },
  {
    id: 'rollouts', title: 'Updating many edge boxes safely', category: 'Operate',
    summary: 'Staged rollouts, halting on failures, rolling back.',
    body: [
      { p: 'A rollout sends a release to a small share of boxes first (for example 10%), waits until they report success, then moves to the next share. If failures reach your threshold it halts so you can roll back.' },
      { steps: ['Add a release (version, and the artifact digest if it has a file).', 'Select the boxes, set stages such as 10,50,100, and create the rollout.', 'Press start. Watch the progress bar. Press advance when a stage is clean.', 'If something fails, pause or abort, then roll back to the earlier release.'] },
      { note: 'The engine is unit-tested. No rollout has run on a real box yet.' },
      { link: ['Fleet updates', '/fleet-updates'] },
    ],
  },
  {
    id: 'health', title: 'Reading System health and Dev tools', category: 'Operate',
    summary: 'What the charts measure and what they do not.',
    body: [
      { p: 'System health charts the API (requests, errors, latency), the database (size, connections, ping), telemetry ingest per minute, alerts and process memory. It samples every 60 seconds and keeps 14 days.' },
      { p: 'Dev tools lists warning and error lines from the API with secrets removed, with filters and search, and downloads a support bundle you can send to support.' },
      { note: 'Not measured: other services\' logs, per-container CPU and memory, MQTT messages per second. The page lists these so nothing is implied.' },
      { link: ['System health', '/system'] }, { link: ['Dev tools', '/dev-tools'] },
    ],
  },
  {
    id: 'control-safety', title: 'Why controlling equipment needs two people', category: 'Security',
    summary: 'Approvals, four-eyes and what the assistant can never do.',
    body: [
      { p: 'Any command that changes something physical needs a request and then an approval by a different person. The assistant and API keys can request nothing that actuates and can never approve. Writes through protocols are disabled by default.' },
      { steps: ['A person requests a command on Control with a reason.', 'A second person reviews and approves, with an authenticator code if your workspace requires it.', 'The edge box checks its own allowed-commands list before acting.', 'Every step is in the audit log.'] },
      { link: ['Control', '/commands'] }, { link: ['Audit', '/audit'] },
    ],
  },
  {
    id: 'assistant', title: 'Using the AI assistant', category: 'Operate',
    summary: 'What it can do, what needs your confirmation, local vs hosted models.',
    body: [
      { p: 'Ask questions about your devices and alerts in plain language. Numbers come from your data, not from the model. Anything that changes something (like creating a site) is shown to you first and needs your confirmation.' },
      { p: 'It can run on a small model on your own machine, or on a larger hosted model if you add one under Settings, AI providers. A hosted model sends your question to that provider; the local one sends nothing out. Off by default.' },
      { link: ['Assistant', '/assistant'] },
    ],
  },
  {
    id: 'glossary', title: 'Glossary', category: 'Reference',
    summary: 'Short definitions of the words used in the app.',
    body: [
      { p: 'Site: a place you group devices by. Edge box (gateway): the small computer that reads sensors. Claim code: one-time code that connects an edge box to your server. Profile: the map that says what a sensor\'s registers mean. Point: one value from a device, such as voltage. Campaign (rollout): sending a release to boxes in stages. Four-eyes: two different people must approve an action.' },
    ],
  },
];

export function searchGuides(q: string): Guide[] {
  const t = q.trim().toLowerCase();
  if (!t) return GUIDES;
  const words = t.split(/\s+/);
  const text = (g: Guide) => (g.title + ' ' + g.summary + ' ' + g.body.map((b) => ('h' in b ? b.h : 'p' in b ? b.p : 'steps' in b ? b.steps.join(' ') : 'note' in b ? b.note : b.link[0])).join(' ')).toLowerCase();
  return GUIDES.filter((g) => words.every((w) => text(g).includes(w)));
}
