-- +goose Up
-- Keep the first event exactly as received (FIX line, XML document...) so the
-- UI can show it and the rule editor can replay it through the same parser.
ALTER TABLE alerts ADD COLUMN sample_raw text NOT NULL DEFAULT '';
ALTER TABLE alerts ADD COLUMN sample_format text NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE alerts DROP COLUMN sample_format;
ALTER TABLE alerts DROP COLUMN sample_raw;
