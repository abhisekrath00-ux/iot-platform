-- A claim is not proof of execution. Never replay ambiguous effects automatically.
ALTER TABLE assistant_actions DROP CONSTRAINT IF EXISTS assistant_actions_status_check;
ALTER TABLE assistant_actions ADD CONSTRAINT assistant_actions_status_check
  CHECK (status IN ('pending','executing','executed','failed','rejected','expired','outcome_unknown'));
-- Legacy claims were labelled executed before the handler ran. A missing result is not success.
UPDATE assistant_actions SET status='outcome_unknown' WHERE status='executed' AND result_code IS NULL;
