export default function Settings() {
  return (
    <>
      <h1>Settings</h1>
      <div className="cards">
        <div className="card"><b>Notifications</b><p className="muted">Connect your SMTP mail server and Slack workspace for alert delivery.</p></div>
        <div className="card"><b>Users & roles</b><p className="muted">Admin, operator, installer, viewer. Tenant-scoped, SSO/OIDC before GA.</p></div>
        <div className="card"><b>Gateways</b><p className="muted">Enroll by one-time claim code, rotate certificates, revoke lost hardware.</p></div>
        <div className="card"><b>Audit log</b><p className="muted">Append-only record of every change and control action.</p></div>
      </div>
    </>
  );
}
