-- +goose Up
CREATE TABLE post_agent_controls (
    post_id BIGINT NOT NULL REFERENCES posts(id),
    owner_id BIGINT NOT NULL REFERENCES users(id),
    mode TEXT NOT NULL DEFAULT 'agent' CHECK (mode IN ('agent', 'manual')),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (post_id, owner_id)
);

-- +goose Down
DROP TABLE post_agent_controls;
