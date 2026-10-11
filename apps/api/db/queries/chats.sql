-- name: SaveAgentChat :exec
INSERT INTO agent_chats (user_id, profile, id, title, directory, provider, model, created_at, updated_at, archived, snapshot, revision)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
ON CONFLICT (user_id, profile, id) DO UPDATE SET
    title = EXCLUDED.title, provider = EXCLUDED.provider, model = EXCLUDED.model,
    updated_at = EXCLUDED.updated_at, archived = EXCLUDED.archived, snapshot = EXCLUDED.snapshot,
    revision = EXCLUDED.revision
WHERE agent_chats.revision <= EXCLUDED.revision
    AND agent_chats.directory = EXCLUDED.directory AND agent_chats.created_at = EXCLUDED.created_at;

-- name: GetAgentChat :one
SELECT snapshot FROM agent_chats WHERE user_id = $1 AND profile = $2 AND id = $3;

-- name: ListAgentChats :many
SELECT id, title, directory, provider, model, created_at, updated_at, archived
FROM agent_chats WHERE user_id = $1 AND profile = $2
ORDER BY updated_at DESC, id LIMIT 200;
