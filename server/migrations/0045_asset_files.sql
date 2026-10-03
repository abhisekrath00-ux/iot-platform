-- Files attached to an asset (manuals, drawings, photos). Stored in the database so an air-gapped
-- install needs no object store; size is capped in the API.
CREATE TABLE IF NOT EXISTS asset_files (
  id TEXT PRIMARY KEY,
  tenant_id TEXT NOT NULL REFERENCES tenants(id),
  asset_id TEXT NOT NULL REFERENCES assets(id) ON DELETE CASCADE,
  filename TEXT NOT NULL,
  content_type TEXT NOT NULL,
  size_bytes INT NOT NULL,
  sha256 TEXT NOT NULL,
  data BYTEA NOT NULL,
  uploaded_by TEXT,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS asset_files_asset ON asset_files (tenant_id, asset_id);
