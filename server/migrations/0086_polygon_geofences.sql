-- Polygon zones: a geofence may carry a polygon (jsonb array of [lat, lon]
-- vertices, 3-64) instead of a circle. lat/lon/radius_m stay NOT NULL: for a
-- polygon zone they hold the vertex average and bounding radius, used only for
-- map framing - membership is the polygon whenever polygon IS NOT NULL.
ALTER TABLE geofences ADD COLUMN IF NOT EXISTS polygon JSONB;
