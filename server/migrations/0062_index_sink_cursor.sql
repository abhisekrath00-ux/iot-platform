-- Position of the optional telemetry index sink (OpenSearch / Elasticsearch). Unused unless INDEX_SINK_URL is set.
CREATE TABLE IF NOT EXISTS index_sink_cursor (
  name TEXT PRIMARY KEY,
  received_at TIMESTAMPTZ NOT NULL,
  event_id TEXT NOT NULL,
  pushed BIGINT NOT NULL DEFAULT 0,
  last_error TEXT NOT NULL DEFAULT '',
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
