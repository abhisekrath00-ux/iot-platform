-- Addresses edge agents and devices use to reach this platform. site_id '' = workspace-wide.
CREATE TABLE IF NOT EXISTS server_endpoints (
  tenant_id TEXT NOT NULL REFERENCES tenants(id),
  id TEXT NOT NULL,
  name TEXT NOT NULL,
  url TEXT NOT NULL,
  site_id TEXT NOT NULL DEFAULT '',
  priority INT NOT NULL DEFAULT 100,   -- lower = tried first
  created_by TEXT NOT NULL DEFAULT '',
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (tenant_id, id)
);
CREATE UNIQUE INDEX IF NOT EXISTS server_endpoints_url ON server_endpoints (tenant_id, site_id, lower(url));
