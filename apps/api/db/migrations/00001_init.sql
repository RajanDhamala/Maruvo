-- +goose Up

CREATE TABLE users (
    id BIGSERIAL PRIMARY KEY,
    email VARCHAR(255) UNIQUE,
    google_id VARCHAR(255) UNIQUE,
    username VARCHAR(100) NOT NULL,
    avatar VARCHAR(500),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    github_id TEXT UNIQUE,
    github_login TEXT NOT NULL DEFAULT ''
);

CREATE TYPE post_status AS ENUM (
    'open',
    'negotiating',
    'in_progress',
    'completed',
    'cancelled'
);

CREATE TYPE post_level AS ENUM (
    'easy',
    'medium',
    'complex'
);

CREATE TABLE posts (
    id BIGSERIAL PRIMARY KEY,
    user_id BIGINT NOT NULL REFERENCES users(id),
    title VARCHAR(500) NOT NULL,
    cost_lamports BIGINT NOT NULL,
    end_time TIMESTAMPTZ NOT NULL,
    status post_status NOT NULL DEFAULT 'open',
    level post_level NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    accepted_by BIGINT REFERENCES users(id),
    accepted_at TIMESTAMPTZ,
    poster_wallet TEXT NOT NULL DEFAULT '',
    worker_wallet TEXT NOT NULL DEFAULT '',
    description TEXT NOT NULL DEFAULT '',
    acceptance_criteria TEXT NOT NULL DEFAULT '',
    input_files TEXT[] NOT NULL DEFAULT '{}',
    expected_outputs TEXT[] NOT NULL DEFAULT '{}',
    reopened_as BIGINT UNIQUE REFERENCES posts(id) ON DELETE SET NULL,
    funding_window_seconds BIGINT NOT NULL DEFAULT 0,
    fund_by TIMESTAMPTZ,
    deliver_by TIMESTAMPTZ,
    review_window_seconds BIGINT NOT NULL DEFAULT 0,
    CONSTRAINT different_worker CHECK (accepted_by IS NULL OR accepted_by <> user_id),
    CONSTRAINT different_reopened_task CHECK (reopened_as IS NULL OR reopened_as <> id),
    CONSTRAINT task_timing CHECK (
        (funding_window_seconds = 0 AND review_window_seconds = 0 AND deliver_by IS NULL AND fund_by IS NULL)
        OR (funding_window_seconds BETWEEN 60 AND 2592000
            AND review_window_seconds BETWEEN 60 AND 2592000
            AND deliver_by IS NOT NULL
            AND deliver_by > end_time + funding_window_seconds * INTERVAL '1 second'))
);

CREATE TABLE wallets (
    user_id BIGINT PRIMARY KEY REFERENCES users(id),
    address TEXT NOT NULL UNIQUE,
    linked_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE wallet_challenges (
    user_id BIGINT PRIMARY KEY REFERENCES users(id),
    address TEXT NOT NULL,
    message TEXT NOT NULL,
    expires_at TIMESTAMPTZ NOT NULL
);

CREATE TABLE post_escrows (
    post_id BIGINT PRIMARY KEY REFERENCES posts(id),
    address TEXT NOT NULL UNIQUE,
    program_id TEXT NOT NULL,
    reviewer TEXT NOT NULL,
    network TEXT NOT NULL,
    transaction TEXT NOT NULL,
    signature TEXT NOT NULL DEFAULT '',
    last_valid_block_height BIGINT NOT NULL,
    fee_lamports BIGINT NOT NULL,
    storage_lamports BIGINT NOT NULL,
    state TEXT NOT NULL DEFAULT 'prepared'
        CHECK (state IN ('prepared', 'pending', 'confirmed', 'failed', 'expired', 'released', 'refunded')),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    agreement_version INTEGER NOT NULL DEFAULT 1 CHECK (agreement_version IN (1, 2, 3)),
    signed_transaction TEXT NOT NULL DEFAULT ''
);

CREATE TABLE post_workspaces (
    post_id BIGINT PRIMARY KEY REFERENCES posts(id),
    last_event_id BIGINT NOT NULL DEFAULT 0,
    submitted_at TIMESTAMPTZ,
    submission TEXT NOT NULL DEFAULT '',
    review_state TEXT NOT NULL DEFAULT 'working'
        CHECK (review_state IN ('working', 'submitted', 'changes_requested', 'approved', 'refunded')),
    submission_version BIGINT NOT NULL DEFAULT 0,
    review_note TEXT NOT NULL DEFAULT '',
    delivery_files UUID[] NOT NULL DEFAULT '{}',
    review_by TIMESTAMPTZ
);

CREATE TABLE workspace_events (
    post_id BIGINT NOT NULL REFERENCES post_workspaces(post_id),
    id BIGINT NOT NULL,
    actor_id BIGINT REFERENCES users(id),
    kind TEXT NOT NULL,
    data JSONB NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    stream_id TEXT,
    PRIMARY KEY (post_id, id)
);
CREATE UNIQUE INDEX workspace_events_stream ON workspace_events(post_id, stream_id);
CREATE INDEX workspace_events_unpublished ON workspace_events(post_id, id) WHERE stream_id IS NULL;

CREATE TABLE agent_grants (
    id UUID PRIMARY KEY,
    owner_id BIGINT NOT NULL REFERENCES users(id),
    post_id BIGINT NOT NULL REFERENCES posts(id),
    name TEXT NOT NULL CHECK (length(name) BETWEEN 1 AND 80),
    token_hash TEXT NOT NULL UNIQUE,
    permissions TEXT[] NOT NULL CHECK (
        cardinality(permissions) BETWEEN 1 AND 5
        AND permissions @> ARRAY['read']::text[]
        AND permissions <@ ARRAY['read', 'message', 'upload', 'submit', 'request-changes']::text[]
    ),
    expires_at TIMESTAMPTZ NOT NULL,
    revoked_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CHECK (expires_at > created_at)
);
CREATE INDEX agent_grants_owner_task ON agent_grants(owner_id, post_id, created_at DESC);

CREATE TABLE workspace_files (
    id UUID PRIMARY KEY,
    post_id BIGINT NOT NULL REFERENCES post_workspaces(post_id),
    uploaded_by BIGINT NOT NULL REFERENCES users(id),
    name TEXT NOT NULL,
    size BIGINT NOT NULL CHECK (size > 0 AND size <= 10485760),
    sha256 TEXT NOT NULL,
    content BYTEA NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    purpose TEXT NOT NULL DEFAULT 'shared' CHECK (purpose IN ('input', 'output', 'shared')),
    agent_grant_id UUID REFERENCES agent_grants(id),
    CHECK (octet_length(content) = size)
);
CREATE INDEX workspace_files_post ON workspace_files(post_id, created_at);

CREATE TABLE post_settlements (
    post_id BIGINT PRIMARY KEY REFERENCES post_workspaces(post_id),
    reviewer_id BIGINT NOT NULL REFERENCES users(id),
    action TEXT NOT NULL CHECK (action IN ('release', 'refund')),
    submission_version BIGINT NOT NULL,
    note TEXT NOT NULL,
    transaction TEXT NOT NULL,
    signed_transaction TEXT NOT NULL DEFAULT '',
    signature TEXT NOT NULL DEFAULT '',
    last_valid_block_height BIGINT NOT NULL,
    fee_lamports BIGINT NOT NULL,
    state TEXT NOT NULL DEFAULT 'prepared' CHECK (state IN ('prepared', 'pending', 'confirmed', 'failed', 'expired')),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE task_deadline_notices (
    post_id BIGINT NOT NULL REFERENCES post_workspaces(post_id),
    stage TEXT NOT NULL CHECK (stage IN ('fund', 'deliver', 'review')),
    submission_version BIGINT NOT NULL,
    due_at TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (post_id, stage, submission_version)
);

-- +goose StatementBegin
CREATE FUNCTION workspace_event(task BIGINT, actor BIGINT, event_kind TEXT, payload JSONB)
RETURNS BIGINT LANGUAGE plpgsql AS $$
DECLARE event_id BIGINT;
BEGIN
    INSERT INTO post_workspaces(post_id) VALUES (task) ON CONFLICT DO NOTHING;
    UPDATE post_workspaces SET last_event_id = last_event_id + 1
        WHERE post_id = task RETURNING last_event_id INTO event_id;
    INSERT INTO workspace_events(post_id, id, actor_id, kind, data)
        VALUES (task, event_id, actor, event_kind, payload);
    RETURN event_id;
END;
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE FUNCTION post_workspace_event() RETURNS TRIGGER LANGUAGE plpgsql AS $$
BEGIN
    IF OLD.accepted_by IS NULL AND NEW.accepted_by IS NOT NULL THEN
        PERFORM workspace_event(NEW.id, NEW.accepted_by, 'task.accepted',
            jsonb_build_object('worker_id', NEW.accepted_by, 'status', NEW.status));
    ELSIF NEW.accepted_by IS NOT NULL AND OLD.status IS DISTINCT FROM NEW.status THEN
        PERFORM workspace_event(NEW.id, NULL, 'task.status', jsonb_build_object('status', NEW.status));
    END IF;
    RETURN NEW;
END;
$$;
-- +goose StatementEnd
CREATE TRIGGER post_workspace_event AFTER UPDATE ON posts
    FOR EACH ROW EXECUTE FUNCTION post_workspace_event();

-- +goose StatementBegin
CREATE FUNCTION escrow_workspace_event() RETURNS TRIGGER LANGUAGE plpgsql AS $$
BEGIN
    IF TG_OP = 'INSERT' OR OLD.state IS DISTINCT FROM NEW.state THEN
        PERFORM workspace_event(NEW.post_id, NULL, 'escrow.updated',
            jsonb_build_object('state', NEW.state, 'signature', NEW.signature));
    END IF;
    RETURN NEW;
END;
$$;
-- +goose StatementEnd
CREATE TRIGGER escrow_workspace_event AFTER INSERT OR UPDATE ON post_escrows
    FOR EACH ROW EXECUTE FUNCTION escrow_workspace_event();

-- +goose StatementBegin
CREATE FUNCTION settlement_workspace_event() RETURNS TRIGGER LANGUAGE plpgsql AS $$
BEGIN
    IF TG_OP = 'INSERT' OR OLD.state IS DISTINCT FROM NEW.state OR OLD.transaction IS DISTINCT FROM NEW.transaction THEN
        PERFORM workspace_event(NEW.post_id, NEW.reviewer_id, 'settlement.updated',
            jsonb_build_object('state', NEW.state, 'action', NEW.action, 'note', NEW.note,
                'signature', NEW.signature, 'submission_version', NEW.submission_version));
    END IF;
    RETURN NEW;
END;
$$;
-- +goose StatementEnd
CREATE TRIGGER settlement_workspace_event AFTER INSERT OR UPDATE ON post_settlements
    FOR EACH ROW EXECUTE FUNCTION settlement_workspace_event();

-- +goose StatementBegin
CREATE FUNCTION review_workspace_event() RETURNS TRIGGER LANGUAGE plpgsql AS $$
BEGIN
    IF OLD.review_state IS DISTINCT FROM NEW.review_state AND NEW.review_state IN ('approved', 'refunded') THEN
        PERFORM workspace_event(NEW.post_id, NULL, 'review.completed',
            jsonb_build_object('review_state', NEW.review_state));
    END IF;
    RETURN NEW;
END;
$$;
-- +goose StatementEnd
CREATE TRIGGER review_workspace_event AFTER UPDATE ON post_workspaces
    FOR EACH ROW EXECUTE FUNCTION review_workspace_event();

-- +goose StatementBegin
CREATE FUNCTION notify_workspace_event() RETURNS TRIGGER LANGUAGE plpgsql AS $$
BEGIN
    PERFORM pg_notify('workspace_events', '');
    RETURN NEW;
END;
$$;
-- +goose StatementEnd
CREATE TRIGGER notify_workspace_event AFTER INSERT ON workspace_events
    FOR EACH ROW EXECUTE FUNCTION notify_workspace_event();

-- +goose StatementBegin
CREATE FUNCTION archive_workspace_message(task BIGINT, actor BIGINT, cursor TEXT, payload JSONB, sent_at TIMESTAMPTZ)
RETURNS VOID LANGUAGE plpgsql AS $$
DECLARE event_id BIGINT;
BEGIN
    PERFORM 1 FROM post_workspaces WHERE post_id = task FOR UPDATE;
    IF NOT FOUND THEN
        RAISE EXCEPTION 'workspace does not exist';
    END IF;
    IF EXISTS (SELECT 1 FROM workspace_events WHERE post_id = task AND stream_id = cursor) THEN
        RETURN;
    END IF;
    event_id := workspace_event(task, actor, 'message', payload);
    UPDATE workspace_events SET stream_id = cursor, created_at = sent_at
        WHERE post_id = task AND id = event_id;
END;
$$;
-- +goose StatementEnd

-- +goose Down

DROP TABLE IF EXISTS post_settlements;
DROP TABLE IF EXISTS workspace_files;
DROP TABLE IF EXISTS workspace_events;
DROP TABLE IF EXISTS task_deadline_notices;
DROP TABLE IF EXISTS post_workspaces;
DROP TABLE IF EXISTS post_escrows;
DROP TABLE IF EXISTS wallet_challenges;
DROP TABLE IF EXISTS wallets;
DROP TABLE IF EXISTS agent_grants;
DROP TABLE IF EXISTS posts;
DROP TABLE IF EXISTS users;

DROP FUNCTION IF EXISTS archive_workspace_message(BIGINT, BIGINT, TEXT, JSONB, TIMESTAMPTZ);
DROP FUNCTION IF EXISTS notify_workspace_event();
DROP FUNCTION IF EXISTS review_workspace_event();
DROP FUNCTION IF EXISTS settlement_workspace_event();
DROP FUNCTION IF EXISTS escrow_workspace_event();
DROP FUNCTION IF EXISTS post_workspace_event();
DROP FUNCTION IF EXISTS workspace_event(BIGINT, BIGINT, TEXT, JSONB);

DROP TYPE IF EXISTS post_level;
DROP TYPE IF EXISTS post_status;
