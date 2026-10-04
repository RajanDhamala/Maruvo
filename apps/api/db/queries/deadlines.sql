-- name: TaskDeadlineSnapshots :many
SELECT p.id, p.status, p.end_time, p.fund_by, p.deliver_by,
    COALESCE(w.review_state, '')::text AS review_state, w.review_by, w.submitted_at
FROM posts p LEFT JOIN post_workspaces w ON w.post_id = p.id
WHERE p.id = ANY($1::bigint[]);

-- name: OverdueTaskCandidates :many
SELECT p.id FROM posts p JOIN post_workspaces w ON w.post_id = p.id
WHERE p.id > $1 AND (
    (p.status = 'negotiating' AND p.fund_by <= NOW())
    OR (p.status = 'in_progress' AND w.review_state IN ('working', 'changes_requested') AND p.deliver_by <= NOW())
    OR (p.status = 'in_progress' AND w.review_state = 'submitted' AND w.review_by <= NOW()))
ORDER BY p.id LIMIT 100;

-- name: RecordDeadlineNotice :one
INSERT INTO task_deadline_notices(post_id, stage, submission_version, due_at)
VALUES ($1, $2, $3, $4) ON CONFLICT DO NOTHING RETURNING post_id;
