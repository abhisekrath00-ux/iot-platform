-- Optional tenant logo (PNG or JPEG, small). Stored in the database; never SVG, which can carry script.
ALTER TABLE tenant_branding ADD COLUMN IF NOT EXISTS logo BYTEA;
ALTER TABLE tenant_branding ADD COLUMN IF NOT EXISTS logo_type TEXT;
