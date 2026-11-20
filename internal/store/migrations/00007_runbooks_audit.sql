-- +goose Up

-- Runbooks created in the UI/API (files in config/runbooks are loaded directly).
-- source is exactly what the person pasted: YAML, Markdown, a checklist, a
-- link or free text; meta holds id/name/team/attachments when not in it.
CREATE TABLE runbooks (
    id          text PRIMARY KEY,
    source      text NOT NULL,
    meta        jsonb NOT NULL DEFAULT '{}',
    version     int NOT NULL DEFAULT 1,
    updated_by  text NOT NULL DEFAULT '',
    updated_at  timestamptz NOT NULL DEFAULT now()
);

-- One row per execution of a runbook step on an alert: actions, checks, and
-- manual steps someone ticked. A queue for the action executor (claimed with
-- SKIP LOCKED and a lease) and the record of who requested, approved and ran
-- what.
CREATE TABLE action_runs (
    id              text PRIMARY KEY,
    alert_id        text NOT NULL REFERENCES alerts(id) ON DELETE CASCADE,
    runbook_id      text NOT NULL,
    step            int NOT NULL,
    kind            text NOT NULL,               -- text | check | action
    action          text NOT NULL DEFAULT '',
    label           text NOT NULL DEFAULT '',    -- the step as shown, values filled in
    params          jsonb NOT NULL DEFAULT '{}', -- rendered parameters
    status          text NOT NULL,               -- awaiting_approval | queued | running | succeeded | failed | rejected | done | skipped
    risk            text NOT NULL DEFAULT 'low',
    approval        text NOT NULL DEFAULT 'none', -- none | required | four-eyes
    requested_by    text NOT NULL DEFAULT '',
    approved_by     text,
    approved_at     timestamptz,
    decided_by      text,                        -- who rejected or marked done
    reason          text NOT NULL DEFAULT '',
    attempts        int NOT NULL DEFAULT 0,
    max_attempts    int NOT NULL DEFAULT 1,
    repeat_every    bigint NOT NULL DEFAULT 0,   -- ms; low-risk steps that repeat while the alert is open
    not_before      timestamptz NOT NULL DEFAULT now(),
    lease_until     timestamptz,
    notified        boolean NOT NULL DEFAULT false, -- approvers told
    output          text NOT NULL DEFAULT '',
    created_at      timestamptz NOT NULL DEFAULT now(),
    updated_at      timestamptz NOT NULL DEFAULT now(),
    finished_at     timestamptz
);
CREATE INDEX action_runs_alert ON action_runs (alert_id, runbook_id, step, created_at);
CREATE INDEX action_runs_due ON action_runs (not_before) WHERE status IN ('queued', 'running');
CREATE INDEX action_runs_approval ON action_runs (created_at) WHERE status = 'awaiting_approval';

-- Tamper-evident audit trail: append-only, each entry carries the hash of the
-- previous one, so changing or deleting any row breaks the chain from there.
CREATE TABLE audit_log (
    seq        bigint PRIMARY KEY,
    at         timestamptz NOT NULL,
    actor      text NOT NULL,
    action     text NOT NULL,
    subject    text NOT NULL,
    detail     text NOT NULL,  -- JSON, stored as text so the hashed bytes are exact
    prev_hash  text NOT NULL,
    hash       text NOT NULL
);
CREATE INDEX audit_log_subject ON audit_log (subject, seq);

-- +goose Down
DROP TABLE audit_log;
DROP TABLE action_runs;
DROP TABLE runbooks;
