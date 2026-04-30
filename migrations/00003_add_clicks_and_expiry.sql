-- +goose Up
-- +goose StatementBegin
-- click_count: lifetime hit counter for the redirect endpoint. Bumped
-- async (fire-and-forget) on every successful /r/:code so a flaking
-- counter never blocks the redirect.
ALTER TABLE links ADD COLUMN click_count BIGINT NOT NULL DEFAULT 0;

-- expires_at: optional wall-clock deadline. NULL means "never expires".
-- The redirect handler returns 410 Gone past this instant; dedup skips
-- expired (and expiry-bearing) rows so a previously-expiring link can
-- be replaced with a permanent one.
ALTER TABLE links ADD COLUMN expires_at TIMESTAMPTZ;

-- Partial index keeps the read path cheap when most rows have no
-- expiry: only the small subset of expiring rows are indexed. Useful
-- for the dedup lookup (`expires_at IS NULL OR expires_at > now()`)
-- and for any future janitor that prunes expired rows.
CREATE INDEX links_expires_at_idx ON links (expires_at) WHERE expires_at IS NOT NULL;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS links_expires_at_idx;
ALTER TABLE links DROP COLUMN IF EXISTS expires_at;
ALTER TABLE links DROP COLUMN IF EXISTS click_count;
-- +goose StatementEnd

