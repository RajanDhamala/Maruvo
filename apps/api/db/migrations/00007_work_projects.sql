-- +goose Up
-- The project row is the transaction/serialization boundary for its work graph.
CREATE TABLE work_projects (
    id UUID PRIMARY KEY,
    owner_id BIGINT NOT NULL REFERENCES users(id),
    post_id BIGINT UNIQUE REFERENCES posts(id),
    state JSONB NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE TABLE work_artifacts (
    id UUID PRIMARY KEY,
    project_id UUID NOT NULL REFERENCES work_projects(id),
    content BYTEA NOT NULL CHECK (octet_length(content) BETWEEN 1 AND 10485760)
);
CREATE TABLE work_grants (
    id UUID PRIMARY KEY,
    project_id UUID NOT NULL REFERENCES work_projects(id),
    owner_id BIGINT NOT NULL REFERENCES users(id),
    name TEXT NOT NULL,
    token_hash TEXT NOT NULL UNIQUE,
    permissions TEXT[] NOT NULL,
    allowed_members BIGINT[] NOT NULL DEFAULT '{}',
    expires_at TIMESTAMPTZ NOT NULL,
    revoked_at TIMESTAMPTZ
);
CREATE TABLE work_receipts (
    owner_id BIGINT NOT NULL REFERENCES users(id),
    operation_id TEXT NOT NULL,
    request_hash TEXT NOT NULL,
    project_id UUID NOT NULL REFERENCES work_projects(id),
    response JSONB NOT NULL,
    PRIMARY KEY (owner_id, operation_id)
);
CREATE TABLE work_events (
    project_id UUID NOT NULL REFERENCES work_projects(id),
    id BIGINT NOT NULL,
    assignment_id TEXT NOT NULL DEFAULT '',
    kind TEXT NOT NULL,
    actor_id BIGINT NOT NULL REFERENCES users(id),
    grant_id TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY(project_id, id)
);
ALTER TABLE posts ADD COLUMN visibility TEXT NOT NULL DEFAULT 'public'
    CHECK (visibility IN ('public', 'private'));
CREATE TABLE work_subjobs (
    post_id BIGINT PRIMARY KEY REFERENCES posts(id),
    project_id UUID NOT NULL REFERENCES work_projects(id),
    assignment_id TEXT NOT NULL,
    UNIQUE(project_id, assignment_id)
);

-- +goose Down
DROP TABLE work_subjobs;
ALTER TABLE posts DROP COLUMN visibility;
DROP TABLE work_events;
DROP TABLE work_receipts;
DROP TABLE work_grants;
DROP TABLE work_artifacts;
DROP TABLE work_projects;
