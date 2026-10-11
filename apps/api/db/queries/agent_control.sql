-- name: GetAgentControl :one
SELECT * FROM post_agent_controls WHERE post_id = $1 AND owner_id = $2;

-- name: EnsureAgentControl :exec
INSERT INTO post_agent_controls (post_id, owner_id) VALUES ($1, $2) ON CONFLICT DO NOTHING;

-- name: LockAgentControl :one
SELECT * FROM post_agent_controls WHERE post_id = $1 AND owner_id = $2 FOR UPDATE;

-- name: SetAgentControl :one
UPDATE post_agent_controls SET mode = $3, updated_at = clock_timestamp()
WHERE post_id = $1 AND owner_id = $2 RETURNING *;

-- name: RevokeTaskAgentGrants :execrows
UPDATE agent_grants SET revoked_at = NOW()
WHERE post_id = $1 AND owner_id = $2 AND revoked_at IS NULL AND expires_at > NOW();
