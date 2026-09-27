-- Fleet config/update rollout: releases, staged campaigns, per-gateway
-- assignments. Cohorts are deterministic (hash of serial + campaign), so a
-- gateway always lands in the same stage bucket for a given campaign.
CREATE TABLE IF NOT EXISTS fleet_releases (
  id TEXT PRIMARY KEY,
  tenant_id TEXT NOT NULL REFERENCES tenants(id),
  version TEXT NOT NULL,
  artifact_sha256 TEXT,        -- artifact digest; in air-gapped sites the artifact ships inside the offline bundle
  notes TEXT,
  created_by TEXT NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE(tenant_id, version)
);
CREATE TABLE IF NOT EXISTS fleet_campaigns (
  id TEXT PRIMARY KEY,
  tenant_id TEXT NOT NULL REFERENCES tenants(id),
  release_id TEXT NOT NULL REFERENCES fleet_releases(id),
  name TEXT NOT NULL,
  stages JSONB NOT NULL,        -- ascending percents, last must be 100, e.g. [10,50,100]
  stage_index INT NOT NULL DEFAULT -1,  -- -1 = not started
  state TEXT NOT NULL DEFAULT 'draft' CHECK (state IN ('draft','running','paused','done','aborted')),
  failure_threshold INT NOT NULL DEFAULT 3,
  serials JSONB NOT NULL,       -- candidate gateway serials for this campaign
  created_by TEXT NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE TABLE IF NOT EXISTS fleet_assignments (
  id TEXT PRIMARY KEY,
  campaign_id TEXT NOT NULL REFERENCES fleet_campaigns(id) ON DELETE CASCADE,
  tenant_id TEXT NOT NULL,
  gateway_serial TEXT NOT NULL,
  release_id TEXT NOT NULL REFERENCES fleet_releases(id),
  state TEXT NOT NULL DEFAULT 'pending' CHECK (state IN ('pending','sent','acked','failed','rolled_back')),
  detail TEXT,
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE(campaign_id, gateway_serial)
);
CREATE INDEX IF NOT EXISTS fleet_assignments_campaign ON fleet_assignments(campaign_id, state);
