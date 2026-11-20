-- +goose Up

-- An alert is the deduplicated, stateful view of one problem: many matching
-- events with the same fingerprint fold into one row with a counter.
CREATE TABLE alerts (
    id              text        PRIMARY KEY,           -- ULID
    fingerprint     text        NOT NULL,
    rule_id         text        NOT NULL,
    team            text        NOT NULL,
    status          text        NOT NULL DEFAULT 'open' CHECK (status IN ('open', 'acked', 'resolved')),
    severity        text        NOT NULL,
    color           text        NOT NULL DEFAULT '',
    title           text        NOT NULL,
    body            text        NOT NULL DEFAULT '',
    fields          jsonb       NOT NULL DEFAULT '[]', -- [{label, value}] chosen by the rule's presentation
    source_id       text        NOT NULL,
    first_event_id  text        NOT NULL,
    last_event_id   text        NOT NULL,
    sample          jsonb       NOT NULL,              -- payload of the first event (runbook variables, previews)
    count           bigint      NOT NULL DEFAULT 1,
    first_seen      timestamptz NOT NULL,
    last_seen       timestamptz NOT NULL,
    acked_by        text,
    acked_at        timestamptz,
    resolved_by     text,
    resolved_at     timestamptz,
    created_at      timestamptz NOT NULL DEFAULT now(),
    updated_at      timestamptz NOT NULL DEFAULT now()
);

-- At most one unresolved alert per fingerprint. This is the source of truth for
-- deduplication; the Redis window is only a fast path in front of it.
CREATE UNIQUE INDEX alerts_open_fingerprint ON alerts (fingerprint) WHERE status <> 'resolved';
CREATE INDEX alerts_team_last_seen ON alerts (team, last_seen DESC);
CREATE INDEX alerts_last_seen ON alerts (last_seen DESC);

-- Transactional outbox: rows are written in the same transaction as the alert,
-- then claimed by dispatchers with FOR UPDATE SKIP LOCKED. Nothing is lost if a
-- channel or a dispatcher is down; rows wait and are retried.
CREATE TABLE notifications (
    id                 text        PRIMARY KEY,        -- ULID
    alert_id           text        NOT NULL REFERENCES alerts (id) ON DELETE CASCADE,
    recipient          text        NOT NULL,           -- person id from teams.yaml
    channel            text        NOT NULL,           -- email | sms | phone
    address            text        NOT NULL,
    subject            text        NOT NULL,
    body_text          text        NOT NULL,
    body_html          text        NOT NULL DEFAULT '',
    status             text        NOT NULL DEFAULT 'pending'
                                   CHECK (status IN ('pending', 'sending', 'sent', 'retry', 'dead')),
    attempts           int         NOT NULL DEFAULT 0,
    next_attempt_at    timestamptz NOT NULL DEFAULT now(),
    locked_until       timestamptz,
    last_error         text        NOT NULL DEFAULT '',
    event_received_at  timestamptz NOT NULL,           -- for end-to-end latency
    created_at         timestamptz NOT NULL DEFAULT now(),
    sent_at            timestamptz
);

CREATE INDEX notifications_due ON notifications (next_attempt_at)
    WHERE status IN ('pending', 'retry', 'sending');
CREATE INDEX notifications_alert ON notifications (alert_id);

-- +goose Down
DROP TABLE notifications;
DROP TABLE alerts;
