-- Derived values (KPIs): a name, a safe arithmetic expression over point references, a unit.
CREATE TABLE IF NOT EXISTS kpis (
  id TEXT PRIMARY KEY,
  tenant_id TEXT NOT NULL REFERENCES tenants(id),
  name TEXT NOT NULL,
  expression TEXT NOT NULL,
  unit TEXT NOT NULL DEFAULT '',
  created_by TEXT,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS kpis_tenant ON kpis (tenant_id);
