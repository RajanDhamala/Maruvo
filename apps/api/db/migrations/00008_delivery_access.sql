-- +goose Up
ALTER TABLE post_workspaces ADD COLUMN refund_blocked BOOLEAN NOT NULL DEFAULT FALSE;

CREATE TABLE workspace_file_accesses (
    post_id BIGINT NOT NULL REFERENCES post_workspaces(post_id),
    file_id UUID NOT NULL REFERENCES workspace_files(id),
    user_id BIGINT NOT NULL REFERENCES users(id),
    submission_version BIGINT NOT NULL CHECK (submission_version >= 0),
    agent_grant_id UUID REFERENCES agent_grants(id),
    sha256 TEXT NOT NULL,
    size BIGINT NOT NULL,
    protects_refund BOOLEAN NOT NULL,
    accessed_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (post_id, file_id, user_id, submission_version)
);

-- +goose Down
DROP TABLE workspace_file_accesses;
ALTER TABLE post_workspaces DROP COLUMN refund_blocked;
