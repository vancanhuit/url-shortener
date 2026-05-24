-- +goose Up
-- +goose StatementBegin
-- auto_dedup: marks rows that were created via the automatic deduplication
-- path (auto-generated codes with no expiry). The partial unique index below
-- ensures that at most one live, permanent row can exist per target_url when
-- auto_dedup is true, making the dedup INSERT ... ON CONFLICT atomic across
-- multiple concurrent replicas.
--
-- Only auto-generated, never-expiring rows participate in this index.
-- User-supplied codes and expiring links always insert fresh rows regardless
-- of whether a matching target already exists, so they stay out of the index.
ALTER TABLE links ADD COLUMN auto_dedup BOOLEAN NOT NULL DEFAULT false;

-- Partial unique index covering only the dedup-eligible rows. The
-- expires_at IS NULL and deleted_at IS NULL clauses keep the index small
-- and ensure that soft-deleted or expiring rows do not block a new
-- permanent row for the same target.
CREATE UNIQUE INDEX links_auto_dedup_target_url_idx ON links (target_url)
    WHERE auto_dedup = true AND expires_at IS NULL AND deleted_at IS NULL;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS links_auto_dedup_target_url_idx;
ALTER TABLE links DROP COLUMN IF EXISTS auto_dedup;
-- +goose StatementEnd
