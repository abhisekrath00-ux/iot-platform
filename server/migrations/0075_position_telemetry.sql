-- Live device position from telemetry: a device can name the two points that
-- carry its latitude and longitude. Ingest then keeps devices.lat/lon current,
-- so the map and geofence enter/exit alerts work off reported positions.
ALTER TABLE devices ADD COLUMN IF NOT EXISTS lat_point TEXT;
ALTER TABLE devices ADD COLUMN IF NOT EXISTS lon_point TEXT;
