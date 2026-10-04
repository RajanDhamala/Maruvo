-- name: CancelUnfundedPost :one
UPDATE posts SET status = 'cancelled', updated_at = NOW()
WHERE id = $1 AND accepted_by IS NOT NULL AND status = 'negotiating'
RETURNING *;

-- name: LinkReopenedPost :one
UPDATE posts SET reopened_as = $2, updated_at = NOW()
WHERE id = $1 AND status = 'cancelled' AND reopened_as IS NULL
RETURNING *;
