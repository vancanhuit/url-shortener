-- +goose Up
-- +goose StatementBegin
-- deleted_at: optional soft-delete tombstone. NULL means "not deleted";
-- a non-NULL timestamp means the link is retired. The redirect handler
-- returns 410 Gone past this column the same way it does for expires_at,
-- so a soft-deleted link looks identical to an expired one to the
-- public.
--
-- Soft delete is preferred over a hard DELETE so audit trails (who
-- created what, click history) survive a takedown. The column is
-- intentionally separate from expires_at: an expiring link can also be
-- deleted, and the two predicates compose without either column having
-- to encode the other's meaning.
--
-- The original UNIQUE constraint on `code` is left in place: even after
-- soft delete, the code stays reserved. That keeps the schema simple
-- (no partial unique index gymnastics) and avoids the security
-- footgun where a previously-shortened URL's code could be re-issued
-- to a different target. If code reuse is ever wanted, a follow-up
-- migration can drop the constraint and replace it with
-- `UNIQUE (code) WHERE deleted_at IS NULL`.
ALTER TABLE links ADD COLUMN deleted_at TIMESTAMPTZ;

-- Partial index restricted to the small subset of deleted rows. Keeps
-- the dedup / list / lookup hot paths -- which all filter
-- `deleted_at IS NULL` -- cheap by leaving the bulk of the table
-- unindexed for this column. Useful for any future janitor that
-- prunes deleted rows on a schedule.
CREATE INDEX links_deleted_at_idx ON links (deleted_at) WHERE deleted_at IS NOT NULL;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS links_deleted_at_idx;
ALTER TABLE links DROP COLUMN IF EXISTS deleted_at;
-- +goose StatementEnd
