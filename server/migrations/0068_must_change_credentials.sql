-- First-run credentials: the installer seeds a known default administrator on a FRESH install only, flagged here.
-- Existing users keep false, so an update never forces or resets anyone.
ALTER TABLE users ADD COLUMN IF NOT EXISTS must_change_credentials BOOLEAN NOT NULL DEFAULT false;
