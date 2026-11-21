-- +goose Up

-- People and teams created or edited in the UI (preferences matrix, onboarding
-- wizard). They are merged with config/teams.yaml: a row with the id of a file
-- entry replaces it until the row is deleted, other rows add new entries.
CREATE TABLE directory_entries (
    kind        text        NOT NULL CHECK (kind IN ('person', 'team')),
    id          text        NOT NULL,
    body        jsonb       NOT NULL,
    version     int         NOT NULL DEFAULT 1,
    updated_by  text        NOT NULL DEFAULT '',
    updated_at  timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (kind, id)
);

-- +goose Down
DROP TABLE directory_entries;
