-- Fragments are versioned so flows can reference them live at a pinned version.
ALTER TABLE flow_fragments ADD COLUMN IF NOT EXISTS version INT NOT NULL DEFAULT 1;
CREATE TABLE IF NOT EXISTS flow_fragment_versions (
  fragment_id TEXT NOT NULL REFERENCES flow_fragments(id) ON DELETE CASCADE,
  version INT NOT NULL,
  graph JSONB NOT NULL,
  created_by TEXT,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (fragment_id, version)
);
INSERT INTO flow_fragment_versions(fragment_id, version, graph, created_by)
  SELECT id, version, graph, created_by FROM flow_fragments ON CONFLICT DO NOTHING;
