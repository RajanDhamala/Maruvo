-- name: RecordWorkspaceFileAccess :one
INSERT INTO workspace_file_accesses(post_id, file_id, user_id, submission_version, agent_grant_id, sha256, size, protects_refund)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8)
ON CONFLICT DO NOTHING RETURNING *;

-- name: BlockWorkspaceRefund :exec
UPDATE post_workspaces SET refund_blocked=TRUE WHERE post_id=$1 AND NOT refund_blocked;

-- name: ExpirePreparedRefund :exec
UPDATE post_settlements SET state='expired', updated_at=NOW()
WHERE post_id=$1 AND action='refund' AND state='prepared';
