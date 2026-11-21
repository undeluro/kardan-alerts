-- +goose Up
ALTER TABLE action_runs ADD COLUMN requested_by_id text NOT NULL DEFAULT '';
ALTER TABLE proposals ADD COLUMN created_by_id text NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE action_runs DROP COLUMN requested_by_id;
ALTER TABLE proposals DROP COLUMN created_by_id;
