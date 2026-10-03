-- User management and optional local sign-in (see docs/saas-design.md).
ALTER TABLE users ADD COLUMN IF NOT EXISTS disabled_at TIMESTAMPTZ;
ALTER TABLE users ADD COLUMN IF NOT EXISTS password_hash TEXT;
ALTER TABLE users ADD COLUMN IF NOT EXISTS failed_logins INT NOT NULL DEFAULT 0;
ALTER TABLE users ADD COLUMN IF NOT EXISTS locked_until TIMESTAMPTZ;
ALTER TABLE users ADD COLUMN IF NOT EXISTS last_login_at TIMESTAMPTZ;
-- One account per address regardless of case, so sign-in by lower(email) is unambiguous. Skipped (with a
-- notice) if a database already holds case-different duplicates; resolve them by hand and restart.
DO $$ BEGIN
  CREATE UNIQUE INDEX IF NOT EXISTS users_email_lower ON users (lower(email));
EXCEPTION WHEN unique_violation THEN
  RAISE NOTICE 'users_email_lower not created: duplicate emails differing only by case exist';
END $$;
