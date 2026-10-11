-- +goose Up
CREATE TABLE agent_chats (
    user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    profile TEXT NOT NULL,
    id TEXT NOT NULL CHECK (id ~ '^[0-9a-f]{32}$'),
    title TEXT NOT NULL,
    directory TEXT NOT NULL,
    provider TEXT NOT NULL,
    model TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL,
    revision BIGINT NOT NULL,
    archived BOOLEAN NOT NULL DEFAULT false,
    snapshot JSONB NOT NULL CHECK (jsonb_typeof(snapshot) = 'object'),
    PRIMARY KEY (user_id, profile, id)
);
CREATE INDEX agent_chats_recent ON agent_chats(user_id, profile, updated_at DESC);

-- +goose Down
DROP TABLE agent_chats;
