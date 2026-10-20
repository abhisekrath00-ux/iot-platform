-- Per-axis position fixes: latitude and longitude arrive as independent
-- readings, so each axis keeps its own value and observation time. A map
-- position (devices.lat/lon) is published only from a pair whose axes agree
-- within a short window, and an older or replayed reading never moves an
-- axis backwards. See internal/store/position.go.
ALTER TABLE devices ADD COLUMN IF NOT EXISTS lat_fix DOUBLE PRECISION;
ALTER TABLE devices ADD COLUMN IF NOT EXISTS lat_fix_at TIMESTAMPTZ;
ALTER TABLE devices ADD COLUMN IF NOT EXISTS lon_fix DOUBLE PRECISION;
ALTER TABLE devices ADD COLUMN IF NOT EXISTS lon_fix_at TIMESTAMPTZ;
