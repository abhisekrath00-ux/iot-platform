-- Typed relations between assets and devices (ThingsBoard "entity relations"): feeds, powers,
-- backs_up, depends_on... A relation is directed: from --relation--> to. Idempotent.
CREATE TABLE IF NOT EXISTS entity_relations (
  tenant_id TEXT NOT NULL REFERENCES tenants(id),
  from_kind TEXT NOT NULL CHECK (from_kind IN ('asset','device')),
  from_id TEXT NOT NULL,
  relation TEXT NOT NULL CHECK (relation ~ '^[a-z][a-z0-9_]{0,31}$'),
  to_kind TEXT NOT NULL CHECK (to_kind IN ('asset','device')),
  to_id TEXT NOT NULL,
  created_by TEXT NOT NULL DEFAULT '',
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (tenant_id, from_kind, from_id, relation, to_kind, to_id),
  CHECK (NOT (from_kind = to_kind AND from_id = to_id))
);
CREATE INDEX IF NOT EXISTS entity_relations_to ON entity_relations (tenant_id, to_kind, to_id);
