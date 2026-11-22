-- EXPERIMENT ONLY: not a migration, not installed by startup or any route.
-- Single atomic DO statement; exercised ONLY in an isolated acceptance fixture.
-- Callers may hold an explicit transaction to keep the SQL fence until commit.
-- Adoption/rollback/restore, old images, database privileges and operator UX
-- require separate acceptance. Dropping this guard can immediately rearm rows.
-- Legacy failed/expired rows are ambiguous and must NEVER be auto-reclassified.
-- Fence SQL writers before the preflight; this does NOT stop in-flight MQTT.
DO $adoption$ BEGIN
 LOCK TABLE gateway_config_pushes IN SHARE ROW EXCLUSIVE MODE;
 IF EXISTS (SELECT 1 FROM gateway_config_pushes WHERE status IN ('failed','expired')) THEN
  RAISE EXCEPTION 'candidate refuses unresolved legacy failed/expired history';
 END IF;

-- Independent of an API's SELECT/count or gateway-lock implementation.
-- Existing multiple open rows make installation fail atomically, not pick one.
CREATE UNIQUE INDEX apply_open_attempt_candidate
 ON gateway_config_pushes (tenant_id,gateway_id)
 WHERE status IN ('sent','applied');

EXECUTE $definition$ CREATE FUNCTION apply_attempt_guard_candidate() RETURNS trigger
LANGUAGE plpgsql AS $guard$ BEGIN
 IF TG_OP='DELETE' THEN
  IF OLD.status IN ('sent','applied') THEN
   RAISE EXCEPTION 'candidate refuses deletion of an unresolved Apply';
  END IF;
  RETURN OLD;
 END IF;
 IF TG_OP='INSERT' THEN
  IF NEW.status <> 'sent' THEN
   RAISE EXCEPTION 'candidate requires a new Apply to start unresolved';
  END IF;
  RETURN NEW;
 END IF;
 IF (NEW.id,NEW.tenant_id,NEW.gateway_id,NEW.version,NEW.devices_yaml,NEW.sha256,NEW.requested_by)
   IS DISTINCT FROM
    (OLD.id,OLD.tenant_id,OLD.gateway_id,OLD.version,OLD.devices_yaml,OLD.sha256,OLD.requested_by) THEN
  RAISE EXCEPTION 'candidate refuses mutation of Apply identity or approved content';
 END IF;
 -- Legacy APIs ignore expiry/update errors. Preserve the original open row
 -- rather than remove it from their admission SELECT, and preserve its detail.
 -- Returning OLD also avoids claiming fresh progress via updated_at.
 IF OLD.status='sent' AND NEW.status IN ('failed','expired') THEN
  RETURN OLD;
 END IF;
 IF (OLD.status='sent' AND NEW.status IN ('sent','applied','rejected')) OR
    (OLD.status='applied' AND NEW.status IN ('applied','confirmed','rolled_back')) OR
    (OLD.status IN ('confirmed','rolled_back','rejected') AND NEW.status=OLD.status) THEN
  RETURN NEW;
 END IF;
 RAISE EXCEPTION 'candidate refuses unsupported Apply state transition';
END $guard$ $definition$;
CREATE TRIGGER apply_attempt_guard_candidate BEFORE INSERT OR UPDATE OR DELETE
 ON gateway_config_pushes FOR EACH ROW EXECUTE FUNCTION apply_attempt_guard_candidate();
END $adoption$;
