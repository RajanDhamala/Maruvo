-- name: IsPostReviewer :one
SELECT EXISTS(SELECT 1 FROM post_escrows e JOIN wallets w ON w.address = e.reviewer
    WHERE e.post_id = $1 AND w.user_id = $2)::boolean AS allowed;

-- name: RequestWorkspaceChanges :one
UPDATE post_workspaces SET review_state = 'changes_requested', review_note = $2, review_by = NULL
WHERE post_id = $1 AND review_state = 'submitted' RETURNING *;

-- name: CloseWorkspaceReview :exec
UPDATE post_workspaces SET review_state = $2, review_by = NULL
WHERE post_id = $1 AND review_state <> $2;

-- name: GetPostSettlement :one
SELECT * FROM post_settlements WHERE post_id = $1;

-- name: SavePostSettlement :one
INSERT INTO post_settlements (post_id, reviewer_id, action, submission_version, note, transaction, last_valid_block_height, fee_lamports)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
ON CONFLICT (post_id) DO UPDATE SET reviewer_id = EXCLUDED.reviewer_id, action = EXCLUDED.action,
    submission_version = EXCLUDED.submission_version, note = EXCLUDED.note,
    transaction = EXCLUDED.transaction, last_valid_block_height = EXCLUDED.last_valid_block_height,
    fee_lamports = EXCLUDED.fee_lamports, signed_transaction = '', signature = '', state = 'prepared', updated_at = NOW()
WHERE post_settlements.state IN ('failed', 'expired')
RETURNING *;

-- name: MarkSettlementSubmitted :one
UPDATE post_settlements SET signature = $2, signed_transaction = $3, state = 'pending', updated_at = NOW()
WHERE post_id = $1 AND state = 'prepared' RETURNING *;

-- name: UpdateSettlementState :exec
UPDATE post_settlements SET state = $2, updated_at = NOW() WHERE post_id = $1 AND state <> $2;

-- name: TrackedEscrows :many
SELECT e.post_id FROM post_escrows e WHERE e.post_id > $1
    AND (e.state IN ('prepared', 'pending', 'confirmed')
        OR EXISTS(SELECT 1 FROM post_settlements s WHERE s.post_id = e.post_id AND s.state IN ('prepared', 'pending')))
ORDER BY e.post_id LIMIT 20;
