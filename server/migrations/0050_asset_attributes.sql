-- Typed attributes on assets (aspects). Definitions say which kind of thing they apply to.
ALTER TABLE assets ADD COLUMN IF NOT EXISTS attributes JSONB NOT NULL DEFAULT '{}'::jsonb;
ALTER TABLE attribute_defs ADD COLUMN IF NOT EXISTS applies_to TEXT NOT NULL DEFAULT 'device';
DO $$ BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'attribute_defs_applies_to_chk') THEN
    ALTER TABLE attribute_defs ADD CONSTRAINT attribute_defs_applies_to_chk CHECK (applies_to IN ('device','asset','both'));
  END IF;
END $$;
