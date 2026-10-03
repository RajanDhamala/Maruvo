-- name: LockWorkspacePublisher :one
SELECT pg_try_advisory_xact_lock(73003101)::boolean AS acquired;

-- name: WorkspaceEventStreamID :one
SELECT stream_id FROM workspace_events WHERE post_id = $1 AND id = $2;

-- name: UnpublishedWorkspaceEvents :many
SELECT * FROM workspace_events WHERE stream_id IS NULL ORDER BY post_id, id LIMIT 100 FOR UPDATE;

-- name: MarkWorkspaceEventPublished :exec
UPDATE workspace_events SET stream_id = $3 WHERE post_id = $1 AND id = $2;

-- name: ArchiveWorkspaceMessages :batchexec
SELECT archive_workspace_message(sqlc.arg(post_id)::bigint, sqlc.narg(actor_id)::bigint,
    sqlc.arg(stream_id)::text, sqlc.arg(data)::jsonb, sqlc.arg(created_at)::timestamptz);
