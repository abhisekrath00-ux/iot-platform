-- STAGED ONLY. Never placed in migrations or run by application startup.
-- Denial is not enrollment, a tenant grant, operator authentication or key storage.
CREATE TABLE admission_denial_epoch_v1 (
 singleton BOOLEAN PRIMARY KEY CHECK (singleton),
 revision BIGINT NOT NULL CHECK (revision >= 0)
);
INSERT INTO admission_denial_epoch_v1(singleton,revision) VALUES(true,0);
CREATE TABLE admission_denied_leaf_v1 (
 leaf_sha256 BYTEA PRIMARY KEY CHECK (octet_length(leaf_sha256)=32),
 denied_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp()
);
