-- +goose Up

-- Suggested changes to rules and runbooks: from people ("propose a fix" on an
-- alert), from Kardan's own fatigue analysis, or drafted with AI. Nothing
-- changes until someone other than the proposer accepts it.
CREATE TABLE proposals (
    id           text        PRIMARY KEY,
    kind         text        NOT NULL CHECK (kind IN ('rule', 'runbook')),
    target       text        NOT NULL,           -- rule or runbook id (new id for additions)
    title        text        NOT NULL,
    rationale    text        NOT NULL DEFAULT '',
    body         text        NOT NULL,           -- proposed YAML / runbook text
    base_version int         NOT NULL DEFAULT 0, -- stored version the proposal was made against (0: file or new)
    source       text        NOT NULL DEFAULT 'person' CHECK (source IN ('person', 'system', 'ai')),
    signal       text        NOT NULL DEFAULT '', -- fatigue signal that produced a system proposal
    alert_id     text,
    status       text        NOT NULL DEFAULT 'open' CHECK (status IN ('open', 'accepted', 'rejected')),
    created_by   text        NOT NULL,
    created_at   timestamptz NOT NULL DEFAULT now(),
    decided_by   text,
    decided_at   timestamptz,
    decision_note text       NOT NULL DEFAULT ''
);

CREATE INDEX proposals_status ON proposals (status, created_at DESC);
-- One open system suggestion per target and signal, so analysis can rerun safely.
CREATE UNIQUE INDEX proposals_open_signal ON proposals (kind, target, signal) WHERE status = 'open' AND signal <> '';

-- +goose Down
DROP TABLE proposals;
