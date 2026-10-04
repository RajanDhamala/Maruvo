-- db/queries/users.sql

-- name: RegisterUser :one
INSERT INTO users (email,google_id,username,avatar)
VALUES ($1, $2, $3, $4)RETURNING *;

-- name: CheckIfUserExist :one
SELECT * FROM users WHERE google_id=$1;

-- name: GetUser :one
SELECT * FROM users WHERE id = $1;

-- name: SignInGitHub :one
INSERT INTO users (github_id, github_login, username, avatar)
VALUES (sqlc.narg(github_id)::text, sqlc.arg(github_login)::text,
    sqlc.arg(github_login)::text, sqlc.narg(avatar)::text)
ON CONFLICT (github_id) DO UPDATE SET github_login = EXCLUDED.github_login,
    avatar = EXCLUDED.avatar
RETURNING *;

-- name: LinkGitHub :one
UPDATE users SET github_id = $2, github_login = $3
WHERE id = $1 AND (github_id IS NULL OR github_id = $2)
RETURNING *;

-- name: PublicUsers :many
SELECT id, username, avatar, github_login FROM users WHERE id = ANY($1::bigint[]);

-- name: CreatePost :one
INSERT INTO posts (user_id,title,cost_lamports,end_time,status,level,description,acceptance_criteria,input_files,expected_outputs,
    funding_window_seconds, deliver_by, review_window_seconds)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)
RETURNING *;

-- name: GetUrPosts :many
SELECT posts.* from posts WHERE posts.user_id=$1 OR posts.accepted_by=$1
    OR EXISTS(SELECT 1 FROM post_escrows e JOIN wallets w ON w.address = e.reviewer
        WHERE e.post_id = posts.id AND w.user_id = $1)
ORDER BY created_at DESC;

-- name: FetchPostByLevel :many
SELECT * from posts WHERE level=$1 AND user_id<>$2 AND status='open'
    AND accepted_by IS NULL AND end_time > NOW() ORDER BY created_at DESC;


-- name: DeleteYourPost :execrows
DELETE from posts WHERE user_id=$1 AND id =$2 AND accepted_by IS NULL;

-- name: UpdatePostStatus :one
UPDATE posts SET status=$1,updated_at=NOW() WHERE id=$2 AND user_id=$3 AND accepted_by IS NULL RETURNING *;
