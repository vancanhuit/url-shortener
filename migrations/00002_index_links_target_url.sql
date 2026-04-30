-- +goose Up
-- +goose StatementBegin
-- target_url index supports the dedup lookup performed by the API: when a
-- user POSTs without a custom code, we first look up an existing row by
-- normalized target and return that code instead of creating a new one.
-- Not a unique constraint -- user-supplied codes can legitimately point
-- different rows at the same target.
CREATE INDEX links_target_url_idx ON links (target_url);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS links_target_url_idx;
-- +goose StatementEnd
