-- points.id was the sole primary key, so two devices could not both have a
-- point named 'kwh' or 'temp' (the second device's points were silently
-- skipped at commissioning). A point is unique per device. Idempotent.
DO $$
BEGIN
  IF (SELECT count(*) FROM pg_index i JOIN pg_attribute a ON a.attrelid = i.indrelid AND a.attnum = ANY (i.indkey)
      WHERE i.indrelid = 'points'::regclass AND i.indisprimary) = 1 THEN
    ALTER TABLE points DROP CONSTRAINT points_pkey;
    ALTER TABLE points ADD PRIMARY KEY (device_id, id);
  END IF;
END $$;
