-- STAGED ONLY. Never copied into /migrations or applied by application startup.
-- This is an admission/execution intent store, not permission to run effects.
CREATE TABLE ingest_processing_v1 (
 event_id TEXT NOT NULL CHECK (octet_length(event_id) BETWEEN 1 AND 256),
 observed_at TIMESTAMPTZ NOT NULL,
 tenant_id TEXT NOT NULL REFERENCES tenants(id),
 envelope BYTEA NOT NULL CHECK (octet_length(envelope) BETWEEN 1 AND 8192),
 plan BYTEA NOT NULL CHECK (octet_length(plan) BETWEEN 1 AND 262144),
 plan_sha256 BYTEA NOT NULL CHECK (octet_length(plan_sha256) = 32),
 policy TEXT NOT NULL CHECK (policy = 'processing-v1'),
 state TEXT NOT NULL DEFAULT 'pending' CHECK (state IN ('pending','executing','completed','outcome_unknown')),
 attempt_id UUID,
 created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
 CHECK ((state = 'pending' AND attempt_id IS NULL) OR (state <> 'pending' AND attempt_id IS NOT NULL)),
 PRIMARY KEY (event_id,observed_at)
);
CREATE INDEX ingest_processing_v1_pending ON ingest_processing_v1(tenant_id,created_at,event_id,observed_at) WHERE state='pending';
