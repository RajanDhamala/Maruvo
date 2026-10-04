-- name: CreateAgentGrant :one
INSERT INTO agent_grants (id, owner_id, post_id, name, token_hash, permissions, expires_at)
VALUES ($1, $2, $3, $4, $5, $6, $7) RETURNING *;

-- name: ResolveAgentGrant :one
SELECT g.* FROM agent_grants g JOIN users u ON u.id = g.owner_id
WHERE g.token_hash = $1 AND g.revoked_at IS NULL AND g.expires_at > NOW();

-- name: LockActiveAgentGrant :one
SELECT * FROM agent_grants WHERE id = $1 AND revoked_at IS NULL AND expires_at > NOW()
FOR SHARE;

-- name: ListAgentGrants :many
SELECT * FROM agent_grants WHERE owner_id = $1 AND post_id = $2 ORDER BY created_at DESC, id LIMIT 100;

-- name: RevokeAgentGrant :one
UPDATE agent_grants SET revoked_at = COALESCE(revoked_at, NOW())
WHERE id = $1 AND owner_id = $2 RETURNING *;
