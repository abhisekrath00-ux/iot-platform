-- Server-side device attributes (labels, serial, firmware notes). Metadata only: never sent to
-- or acted on by a device. Idempotent: migrations re-run on every API start.
ALTER TABLE devices ADD COLUMN IF NOT EXISTS attributes JSONB NOT NULL DEFAULT '{}'::jsonb;
