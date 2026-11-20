-- +goose Up

-- Rules created or edited at runtime through the API/UI. File rules
-- (config/rules/*.yaml) are merged with these; a row here with the same id
-- overrides the file version until it is deleted.
CREATE TABLE rules (
    id          text        PRIMARY KEY,
    body        jsonb       NOT NULL,
    version     int         NOT NULL DEFAULT 1,
    updated_by  text        NOT NULL DEFAULT '',
    created_at  timestamptz NOT NULL DEFAULT now(),
    updated_at  timestamptz NOT NULL DEFAULT now()
);

-- Catalogue of payload structures seen, keyed by structural signature.
CREATE TABLE schemas (
    id             text        PRIMARY KEY,
    name           text        NOT NULL,
    sources        text[]      NOT NULL DEFAULT '{}',
    format         text        NOT NULL DEFAULT '',
    fields         jsonb       NOT NULL DEFAULT '[]',
    auto_facets    jsonb       NOT NULL DEFAULT '{}',
    manual_facets  jsonb       NOT NULL DEFAULT '{}',
    sample         jsonb       NOT NULL,
    sample_raw     text        NOT NULL DEFAULT '',
    count          bigint      NOT NULL DEFAULT 0,
    first_seen     timestamptz NOT NULL,
    last_seen      timestamptz NOT NULL
);

CREATE INDEX schemas_last_seen ON schemas (last_seen DESC);

-- +goose Down
DROP TABLE schemas;
DROP TABLE rules;
