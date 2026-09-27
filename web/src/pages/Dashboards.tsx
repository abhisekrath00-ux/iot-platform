export default function Dashboards() {
  return (
    <>
      <h1>Dashboards & reports</h1>
      <p className="muted">
        The report builder lives here: drag-and-drop widgets (timeseries, KPI,
        table, status), custom dashboards, and exportable report templates.
        Layouts persist via <code>POST /v1/dashboards</code>; scheduled email/Slack
        delivery uses the notification service.
      </p>
    </>
  );
}
