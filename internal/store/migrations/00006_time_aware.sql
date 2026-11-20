-- +goose Up

-- The settlement / payment cut-off an alert races against, and the impact
-- before urgency (impact = base_impact + urgency, refreshed as time passes).
ALTER TABLE alerts ADD COLUMN cutoff_name text;
ALTER TABLE alerts ADD COLUMN cutoff_at timestamptz;
ALTER TABLE alerts ADD COLUMN base_impact int NOT NULL DEFAULT 0;
UPDATE alerts SET base_impact = impact;

-- Escalation ladder progress for unacknowledged alerts.
ALTER TABLE alerts ADD COLUMN escalation_level int NOT NULL DEFAULT 0;
ALTER TABLE alerts ADD COLUMN escalated_at timestamptz;

-- Template values computed when the alert fired (aggregate totals, absence
-- timings), so escalations and digests render the same text later.
ALTER TABLE alerts ADD COLUMN vars jsonb;

CREATE INDEX alerts_open_cutoff ON alerts (cutoff_at) WHERE status <> 'resolved' AND cutoff_at IS NOT NULL;
CREATE INDEX alerts_unacked ON alerts (rule_id, created_at) WHERE status = 'open';

-- +goose Down
DROP INDEX alerts_unacked;
DROP INDEX alerts_open_cutoff;
ALTER TABLE alerts DROP COLUMN vars;
ALTER TABLE alerts DROP COLUMN escalated_at;
ALTER TABLE alerts DROP COLUMN escalation_level;
ALTER TABLE alerts DROP COLUMN base_impact;
ALTER TABLE alerts DROP COLUMN cutoff_at;
ALTER TABLE alerts DROP COLUMN cutoff_name;
