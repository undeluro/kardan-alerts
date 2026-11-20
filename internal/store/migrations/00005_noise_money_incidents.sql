-- +goose Up

-- Money at risk and an impact score, so the feed can sort by what matters.
ALTER TABLE alerts ADD COLUMN notional_usd double precision;
ALTER TABLE alerts ADD COLUMN impact int NOT NULL DEFAULT 0;
-- Grouping windows ("one alert per instrument every 5 minutes").
ALTER TABLE alerts ADD COLUMN window_start timestamptz;
ALTER TABLE alerts ADD COLUMN window_end timestamptz;
-- Correlation into incidents.
ALTER TABLE alerts ADD COLUMN incident_id text;

CREATE INDEX alerts_impact ON alerts (impact DESC, last_seen DESC) WHERE status <> 'resolved';
CREATE INDEX alerts_window_end ON alerts (window_end) WHERE status <> 'resolved' AND window_end IS NOT NULL;
CREATE INDEX alerts_incident ON alerts (incident_id) WHERE incident_id IS NOT NULL;
CREATE INDEX alerts_open_rule_last_seen ON alerts (rule_id, last_seen) WHERE status <> 'resolved';

-- An incident groups alerts that share a business key (counterparty,
-- instrument, service...) within a time window, across teams and sources.
CREATE TABLE incidents (
    id            text        PRIMARY KEY,
    key           text        NOT NULL,           -- "$counterparty=ACME-CAPITAL"
    key_field     text        NOT NULL,
    key_value     text        NOT NULL,
    status        text        NOT NULL DEFAULT 'open' CHECK (status IN ('open', 'resolved')),
    alert_count   int         NOT NULL DEFAULT 0,
    teams         text[]      NOT NULL DEFAULT '{}',
    max_level     int         NOT NULL DEFAULT 0,  -- normalized severity (5 = critical)
    notional_usd  double precision NOT NULL DEFAULT 0,
    impact        int         NOT NULL DEFAULT 0,
    first_seen    timestamptz NOT NULL,
    last_seen     timestamptz NOT NULL,
    resolved_at   timestamptz,
    created_at    timestamptz NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX incidents_open_key ON incidents (key) WHERE status = 'open';
CREATE INDEX incidents_last_seen ON incidents (last_seen DESC);

-- Throttled notifications are 'held' and later folded into one digest
-- ('suppressed'); digests and all-clear messages are their own kinds.
ALTER TABLE notifications DROP CONSTRAINT notifications_status_check;
ALTER TABLE notifications ADD CONSTRAINT notifications_status_check
    CHECK (status IN ('pending', 'sending', 'sent', 'retry', 'dead', 'standby', 'cancelled', 'held', 'suppressed'));
ALTER TABLE notifications ADD COLUMN kind text NOT NULL DEFAULT 'alert'; -- alert | digest | resolved
CREATE INDEX notifications_held ON notifications (next_attempt_at) WHERE status = 'held';

-- +goose Down
DROP INDEX notifications_held;
ALTER TABLE notifications DROP COLUMN kind;
ALTER TABLE notifications DROP CONSTRAINT notifications_status_check;
ALTER TABLE notifications ADD CONSTRAINT notifications_status_check
    CHECK (status IN ('pending', 'sending', 'sent', 'retry', 'dead', 'standby', 'cancelled'));
DROP TABLE incidents;
DROP INDEX alerts_open_rule_last_seen;
DROP INDEX alerts_incident;
DROP INDEX alerts_window_end;
DROP INDEX alerts_impact;
ALTER TABLE alerts DROP COLUMN incident_id;
ALTER TABLE alerts DROP COLUMN window_end;
ALTER TABLE alerts DROP COLUMN window_start;
ALTER TABLE alerts DROP COLUMN impact;
ALTER TABLE alerts DROP COLUMN notional_usd;
