-- name: CreateLink :one
INSERT INTO links (code, target_url, expires_at)
VALUES ($1, $2, $3)
RETURNING id, code, target_url, created_at, click_count, expires_at, deleted_at, auto_dedup;

-- name: CreateAutoLink :one
INSERT INTO links (code, target_url, expires_at, auto_dedup)
VALUES ($1, $2, NULL, true)
ON CONFLICT (target_url)
WHERE auto_dedup = true AND expires_at IS NULL AND deleted_at IS NULL
DO UPDATE SET code = links.code
RETURNING id, code, target_url, created_at, click_count, expires_at, deleted_at, auto_dedup;

-- name: IncrementClicks :exec
UPDATE links SET click_count = click_count + 1 WHERE code = $1;

-- name: ListLinks :many
SELECT id, code, target_url, created_at, click_count, expires_at, deleted_at, auto_dedup
FROM links
WHERE deleted_at IS NULL
  AND (expires_at IS NULL OR expires_at > NOW())
ORDER BY id DESC
LIMIT $1;

-- name: ListLinksBeforeID :many
SELECT id, code, target_url, created_at, click_count, expires_at, deleted_at, auto_dedup
FROM links
WHERE id < $1
  AND deleted_at IS NULL
  AND (expires_at IS NULL OR expires_at > NOW())
ORDER BY id DESC
LIMIT $2;

-- name: GetLinkByCode :one
SELECT id, code, target_url, created_at, click_count, expires_at, deleted_at, auto_dedup
FROM links
WHERE code = $1;

-- name: GetLinkByTargetURL :one
SELECT id, code, target_url, created_at, click_count, expires_at, deleted_at, auto_dedup
FROM links
WHERE target_url = $1
  AND expires_at IS NULL
  AND deleted_at IS NULL
ORDER BY id ASC
LIMIT 1;

-- name: SoftDeleteLink :execresult
UPDATE links
SET deleted_at = now()
WHERE code = $1
  AND deleted_at IS NULL;
