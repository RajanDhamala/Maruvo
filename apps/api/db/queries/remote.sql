-- name: GetRemoteActivity :one
SELECT * FROM remote_activity WHERE post_id = $1;

-- name: UpdateRemoteActivity :one
INSERT INTO remote_activity(post_id, run_id, state, detail, active_until)
VALUES (sqlc.arg(post_id), sqlc.arg(run_id), COALESCE(NULLIF(sqlc.arg(state)::text, ''), 'starting'),
    sqlc.arg(detail), CASE WHEN sqlc.arg(state)::text IN ('waiting_for_review', 'interrupted', 'failed')
        THEN NOW() ELSE NOW() + INTERVAL '90 seconds' END)
ON CONFLICT (post_id) DO UPDATE SET
    run_id = EXCLUDED.run_id,
    state = COALESCE(NULLIF(sqlc.arg(state)::text, ''), remote_activity.state),
    detail = CASE WHEN sqlc.arg(state)::text = '' THEN remote_activity.detail ELSE EXCLUDED.detail END,
    active_until = CASE WHEN sqlc.arg(state)::text IN ('waiting_for_review', 'interrupted', 'failed') THEN NOW() ELSE EXCLUDED.active_until END,
    updated_at = NOW()
WHERE remote_activity.run_id = EXCLUDED.run_id OR remote_activity.active_until <= NOW()
RETURNING *;

-- name: RemoteInbox :many
SELECT p.* FROM posts p
WHERE p.user_id = sqlc.arg(user_id) OR p.accepted_by = sqlc.arg(user_id)
    OR (p.target_worker = sqlc.arg(user_id) AND p.status = 'open' AND p.end_time > NOW())
    OR EXISTS(SELECT 1 FROM post_escrows e JOIN wallets w ON w.address = e.reviewer
        WHERE e.post_id = p.id AND w.user_id = sqlc.arg(user_id))
ORDER BY (p.status NOT IN ('completed', 'cancelled')) DESC, p.updated_at DESC, p.id DESC LIMIT 100;

-- name: RemoteSnapshots :many
SELECT p.id, COALESCE(a.state, '')::text AS activity_state,
    COALESCE(a.detail, '')::text AS detail, a.active_until, a.updated_at AS worker_seen,
    COALESCE(w.review_state, '')::text AS review_state,
    COALESCE(w.submission_version, 0)::bigint AS submission_version,
    COALESCE(w.last_event_id, 0)::bigint AS last_event_id,
    COALESCE(e.state, '')::text AS escrow_state,
    o.available_until AS offer_until, COALESCE(o.name, '')::text AS agent_name
FROM posts p
LEFT JOIN remote_activity a ON a.post_id = p.id
LEFT JOIN post_workspaces w ON w.post_id = p.id
LEFT JOIN post_escrows e ON e.post_id = p.id
LEFT JOIN agent_offers o ON o.user_id = COALESCE(p.accepted_by, p.target_worker)
WHERE p.id = ANY($1::bigint[]);
