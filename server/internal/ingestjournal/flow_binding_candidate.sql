-- CANDIDATE ONLY. Not a migration and never installed by product startup.
-- Explicit disposable fixture only, AFTER candidate.sql. No runtime worker.
-- One retained flow selection per admission. This is NOT complete config binding.
CREATE TABLE ingest_flow_evidence_candidate (
 event_id TEXT NOT NULL,
 observed_at TIMESTAMPTZ NOT NULL,
 tenant_id TEXT NOT NULL,
 evidence BYTEA NOT NULL CHECK (octet_length(evidence) BETWEEN 1 AND 131072),
 evidence_sha256 TEXT NOT NULL CHECK (evidence_sha256 ~ '^[0-9a-f]{64}$'),
 created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
 PRIMARY KEY(event_id,observed_at),
 FOREIGN KEY(event_id,observed_at) REFERENCES ingest_admission_candidate(event_id,observed_at)
);
