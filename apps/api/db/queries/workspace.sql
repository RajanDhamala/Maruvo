-- name: GetWorkspace :one
SELECT * FROM post_workspaces WHERE post_id = $1;

-- name: ListWorkspaceEvents :many
SELECT * FROM workspace_events WHERE post_id = $1 AND id > $2 ORDER BY id LIMIT 100;

-- name: RecentWorkspaceEvents :many
SELECT * FROM (SELECT * FROM workspace_events WHERE post_id = $1 ORDER BY id DESC LIMIT 100) recent ORDER BY id;

-- name: AppendWorkspaceEvent :one
SELECT workspace_event(sqlc.arg(post_id)::bigint, sqlc.narg(actor_id)::bigint,
    sqlc.arg(kind)::text, sqlc.arg(data)::jsonb)::bigint AS id;

-- name: ListWorkspaceFiles :many
SELECT id, post_id, uploaded_by, name, size, sha256, created_at, purpose
FROM workspace_files WHERE post_id = $1 ORDER BY created_at, id;

-- name: WorkspaceFileBytes :one
SELECT COALESCE(SUM(size), 0)::bigint AS size FROM workspace_files WHERE post_id = $1;

-- name: SaveWorkspaceFile :exec
INSERT INTO workspace_files (id, post_id, uploaded_by, name, size, sha256, content, purpose)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8);

-- name: GetWorkspaceFile :one
SELECT * FROM workspace_files WHERE id = $1 AND post_id = $2;

-- name: SubmitWorkspace :one
UPDATE post_workspaces SET submitted_at = NOW(), submission = $2,
    submission_version = submission_version + 1, review_state = 'submitted', review_note = '', delivery_files = $3
WHERE post_id = $1 AND review_state IN ('working', 'changes_requested') RETURNING *;
