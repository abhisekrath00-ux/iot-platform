ALTER TABLE device_profiles ADD COLUMN IF NOT EXISTS connection_defaults jsonb NOT NULL DEFAULT '{}'::jsonb;
