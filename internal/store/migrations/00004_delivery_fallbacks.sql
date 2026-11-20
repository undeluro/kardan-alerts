-- +goose Up

-- Fallback deliveries are rendered (with the right masking) when the alert is
-- created and parked as 'standby'. If the primary delivery keeps failing the
-- dispatcher activates them; if it succeeds they are 'cancelled'.
ALTER TABLE notifications DROP CONSTRAINT notifications_status_check;
ALTER TABLE notifications ADD CONSTRAINT notifications_status_check
    CHECK (status IN ('pending', 'sending', 'sent', 'retry', 'dead', 'standby', 'cancelled'));

ALTER TABLE notifications ADD COLUMN fallback_for text REFERENCES notifications (id) ON DELETE CASCADE;
ALTER TABLE notifications ADD COLUMN reason text NOT NULL DEFAULT ''; -- why this person/channel (on-call, preference...)

CREATE INDEX notifications_fallback_for ON notifications (fallback_for) WHERE fallback_for IS NOT NULL;
CREATE INDEX notifications_status_created ON notifications (status, created_at DESC);

-- +goose Down
DROP INDEX notifications_status_created;
DROP INDEX notifications_fallback_for;
ALTER TABLE notifications DROP COLUMN reason;
ALTER TABLE notifications DROP COLUMN fallback_for;
ALTER TABLE notifications DROP CONSTRAINT notifications_status_check;
ALTER TABLE notifications ADD CONSTRAINT notifications_status_check
    CHECK (status IN ('pending', 'sending', 'sent', 'retry', 'dead'));
