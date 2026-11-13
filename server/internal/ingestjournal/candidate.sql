-- CANDIDATE ONLY. Not in migrations, not installed by product startup.
-- Requires explicit future migration/rollback/previous-image acceptance.
CREATE TABLE ingest_admission_candidate (
 event_id TEXT NOT NULL,
 observed_at TIMESTAMPTZ NOT NULL,
 tenant_id TEXT NOT NULL,
 envelope BYTEA NOT NULL CHECK (octet_length(envelope) <= 8192),
 state TEXT NOT NULL DEFAULT 'pending' CHECK (state = 'pending'),
 created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
 PRIMARY KEY (event_id, observed_at)
);
