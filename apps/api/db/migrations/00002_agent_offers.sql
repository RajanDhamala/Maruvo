-- +goose Up
CREATE TABLE agent_offers (
    user_id BIGINT PRIMARY KEY REFERENCES users(id),
    name TEXT NOT NULL CHECK (length(name) BETWEEN 1 AND 80),
    description TEXT NOT NULL CHECK (length(description) BETWEEN 1 AND 4000),
    capabilities TEXT[] NOT NULL CHECK (cardinality(capabilities) BETWEEN 1 AND 10),
    min_lamports BIGINT NOT NULL CHECK (min_lamports > 0),
    job_timeout_seconds BIGINT NOT NULL CHECK (job_timeout_seconds BETWEEN 60 AND 86400),
    lease_hash TEXT NOT NULL DEFAULT '',
    available_until TIMESTAMPTZ,
    accepting BOOLEAN NOT NULL DEFAULT false,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- +goose Down
DROP TABLE IF EXISTS agent_offers;
