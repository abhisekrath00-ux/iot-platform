-- Per-tenant resource limits. NULL = unlimited. The row with tenant_id '*' is the default for tenants without
-- their own row. Set by the operator (tenantctl quota), never by a tenant's own admin. No payment is involved.
CREATE TABLE IF NOT EXISTS tenant_quotas (
  tenant_id TEXT PRIMARY KEY,
  max_devices INT CHECK (max_devices IS NULL OR max_devices >= 0),
  max_users INT CHECK (max_users IS NULL OR max_users >= 0),
  max_api_keys INT CHECK (max_api_keys IS NULL OR max_api_keys >= 0),
  max_customers INT CHECK (max_customers IS NULL OR max_customers >= 0),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Backstop that holds under concurrency: the handlers check first for a friendly message, and this trigger
-- makes the limit atomic (a per-tenant advisory lock serialises inserts of one resource kind).
CREATE OR REPLACE FUNCTION enforce_tenant_quota() RETURNS trigger AS $$
DECLARE
  lim INT;
  cur BIGINT;
  col TEXT := TG_ARGV[0];
BEGIN
  PERFORM pg_advisory_xact_lock(hashtext('quota:' || TG_TABLE_NAME || ':' || NEW.tenant_id));
  EXECUTE format('SELECT COALESCE((SELECT %I FROM tenant_quotas WHERE tenant_id=$1), (SELECT %I FROM tenant_quotas WHERE tenant_id=''*''))', col, col) INTO lim USING NEW.tenant_id;
  IF lim IS NULL THEN RETURN NEW; END IF;
  IF TG_TABLE_NAME = 'api_keys' THEN -- revoked and expired keys do not count
    SELECT count(*) INTO cur FROM api_keys WHERE tenant_id=NEW.tenant_id AND revoked_at IS NULL AND expires_at > now();
  ELSE
    EXECUTE format('SELECT count(*) FROM %I WHERE tenant_id=$1', TG_TABLE_NAME) INTO cur USING NEW.tenant_id;
  END IF;
  IF cur >= lim THEN
    RAISE EXCEPTION 'quota exceeded: % limit of % reached', TG_TABLE_NAME, lim USING ERRCODE = 'check_violation';
  END IF;
  RETURN NEW;
END $$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS quota_devices ON devices;
CREATE TRIGGER quota_devices BEFORE INSERT ON devices FOR EACH ROW EXECUTE FUNCTION enforce_tenant_quota('max_devices');
DROP TRIGGER IF EXISTS quota_users ON users;
CREATE TRIGGER quota_users BEFORE INSERT ON users FOR EACH ROW EXECUTE FUNCTION enforce_tenant_quota('max_users');
DROP TRIGGER IF EXISTS quota_api_keys ON api_keys;
CREATE TRIGGER quota_api_keys BEFORE INSERT ON api_keys FOR EACH ROW EXECUTE FUNCTION enforce_tenant_quota('max_api_keys');
DROP TRIGGER IF EXISTS quota_customers ON customers;
CREATE TRIGGER quota_customers BEFORE INSERT ON customers FOR EACH ROW EXECUTE FUNCTION enforce_tenant_quota('max_customers');
