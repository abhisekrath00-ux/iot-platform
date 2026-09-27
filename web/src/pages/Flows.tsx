export default function Flows() {
  return (
    <>
      <h1>Flows</h1>
      <p className="muted">
        Visual rules: trigger -&gt; condition -&gt; delay -&gt; notify / approved action.
        Typed ports prevent incompatible units; publish requires validation,
        simulation on recorded data, and is versioned with rollback.
      </p>
    </>
  );
}
