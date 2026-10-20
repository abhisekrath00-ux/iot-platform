-- Per-customer branding: a customer (or sub-customer) may override the tenant's
-- product name, accent colour and logo for users scoped to it. Rows are sparse;
-- a scoped user's /v1/branding resolves customer row -> tenant row -> defaults.
CREATE TABLE IF NOT EXISTS customer_branding (
  customer_id  TEXT PRIMARY KEY REFERENCES customers(id) ON DELETE CASCADE,
  tenant_id    TEXT NOT NULL REFERENCES tenants(id),
  product_name TEXT NOT NULL DEFAULT '',
  accent       TEXT NOT NULL DEFAULT '',
  logo         BYTEA,
  logo_type    TEXT,
  updated_by   TEXT,
  updated_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS customer_branding_tenant ON customer_branding (tenant_id);
