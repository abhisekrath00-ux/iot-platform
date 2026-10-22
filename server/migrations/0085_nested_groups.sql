-- Nested device groups. A group's coverage is its own members plus the
-- members of every descendant group, everywhere a group scope is accepted
-- (fleet filter, alert rules, alert list, dashboard filter). Depth is capped
-- and cycles are rejected at write time in groups.go, the same stance as the
-- asset hierarchy. Deleting a parent lifts its children to top level
-- (ON DELETE SET NULL): no group is ever deleted implicitly.
ALTER TABLE device_groups ADD COLUMN IF NOT EXISTS parent_id TEXT REFERENCES device_groups(id) ON DELETE SET NULL;
CREATE INDEX IF NOT EXISTS device_groups_parent ON device_groups(tenant_id, parent_id);
