-- Asset hierarchy: plant > line > machine, with devices attached to an asset.
CREATE TABLE IF NOT EXISTS assets (
  id TEXT PRIMARY KEY,
  tenant_id TEXT NOT NULL REFERENCES tenants(id),
  parent_id TEXT REFERENCES assets(id),
  name TEXT NOT NULL,
  kind TEXT NOT NULL DEFAULT 'asset',
  created_by TEXT,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS assets_tenant_parent ON assets (tenant_id, parent_id);
ALTER TABLE devices ADD COLUMN IF NOT EXISTS asset_id TEXT REFERENCES assets(id);
CREATE INDEX IF NOT EXISTS devices_asset ON devices (asset_id);
