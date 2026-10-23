-- Global trail retention must find old rows even when devices stop reporting.
CREATE INDEX IF NOT EXISTS device_positions_expiry ON device_positions(recorded_at);
