-- +goose Up
ALTER TABLE posts ADD COLUMN target_worker BIGINT REFERENCES users(id);
ALTER TABLE posts ADD CONSTRAINT different_target CHECK (target_worker IS NULL OR target_worker <> user_id);
CREATE INDEX posts_directed_queue ON posts(target_worker, created_at) WHERE status = 'open';

CREATE TABLE remote_activity (
    post_id BIGINT PRIMARY KEY REFERENCES posts(id),
    run_id TEXT NOT NULL CHECK (length(run_id) BETWEEN 32 AND 128),
    state TEXT NOT NULL CHECK (state IN ('starting', 'working', 'waiting_for_answer', 'waiting_for_review', 'interrupted', 'failed')),
    detail TEXT NOT NULL DEFAULT '' CHECK (length(detail) <= 1000),
    active_until TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- +goose Down
DROP TABLE remote_activity;
ALTER TABLE posts DROP CONSTRAINT different_target;
ALTER TABLE posts DROP COLUMN target_worker;
