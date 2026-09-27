export default function Fleet() {
  return (
    <>
      <h1>Fleet</h1>
      <div className="cards">
        <div className="card"><div className="muted">Gateways</div><div className="kpi">-</div><span className="pill ok">healthy</span></div>
        <div className="card"><div className="muted">Devices</div><div className="kpi">-</div><span className="pill ok">healthy</span></div>
        <div className="card"><div className="muted">Stale readings</div><div className="kpi">-</div><span className="pill warn">watch</span></div>
        <div className="card"><div className="muted">Open alerts</div><div className="kpi">-</div></div>
      </div>
      <p className="muted" style={{ marginTop: 16 }}>
        Fleet health lands here first: what is connected, what is stale, what changed.
        Numbers always carry last-reading time and data quality.
      </p>
    </>
  );
}
