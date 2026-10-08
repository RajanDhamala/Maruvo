-- name: SaveAgentOffer :one
INSERT INTO agent_offers (user_id, name, description, capabilities, min_lamports, job_timeout_seconds)
VALUES ($1, $2, $3, $4, $5, $6)
ON CONFLICT (user_id) DO UPDATE SET name = EXCLUDED.name, description = EXCLUDED.description,
    capabilities = EXCLUDED.capabilities, min_lamports = EXCLUDED.min_lamports,
    job_timeout_seconds = EXCLUDED.job_timeout_seconds, updated_at = NOW()
WHERE agent_offers.available_until IS NULL OR agent_offers.available_until <= NOW()
RETURNING *;

-- name: GetAgentOffer :one
SELECT * FROM agent_offers WHERE user_id = $1;

-- name: LockAgentOffer :one
SELECT * FROM agent_offers WHERE user_id = $1 FOR UPDATE;

-- name: LeaseAgentOffer :one
UPDATE agent_offers SET lease_hash = $2, accepting = $3,
    available_until = NOW() + INTERVAL '90 seconds', updated_at = NOW()
WHERE user_id = $1 AND (lease_hash = $2 OR available_until IS NULL OR available_until <= NOW())
RETURNING *;

-- name: ReleaseAgentOffer :execrows
UPDATE agent_offers SET available_until = NULL, accepting = false, lease_hash = ''
WHERE user_id = $1 AND lease_hash = $2;

-- name: ListAgentOffers :many
SELECT o.user_id, o.name, o.description, o.capabilities, o.min_lamports, o.job_timeout_seconds,
    o.available_until, o.updated_at, u.username, u.github_login,
    COALESCE(o.available_until > NOW(), false)::boolean AS online,
    (COALESCE(o.available_until > NOW(), false) AND o.accepting AND NOT EXISTS (SELECT 1 FROM posts p
        WHERE p.accepted_by = o.user_id AND p.status NOT IN ('completed', 'cancelled')))::boolean AS available
FROM agent_offers o JOIN users u ON u.id = o.user_id
ORDER BY COALESCE(o.available_until > NOW(), false) DESC, o.updated_at DESC LIMIT 100;

-- name: WorkerHasActiveTask :one
SELECT EXISTS (SELECT 1 FROM posts WHERE accepted_by = $1 AND status NOT IN ('completed', 'cancelled'))::boolean;
