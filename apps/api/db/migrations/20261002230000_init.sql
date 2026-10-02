-- +goose Up

CREATE TABLE users (
    id BIGSERIAL PRIMARY KEY,
    email VARCHAR(255) NOT NULL UNIQUE,
    google_id VARCHAR(255) UNIQUE,
    username VARCHAR(100) NOT NULL,
    avatar VARCHAR(500),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
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
    CONSTRAINT different_worker CHECK (accepted_by IS NULL OR accepted_by <> user_id)
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
    state TEXT NOT NULL DEFAULT 'prepared' CHECK (state IN ('prepared', 'pending', 'confirmed', 'failed', 'expired', 'released', 'refunded')),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
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
    delivery_files UUID[] NOT NULL DEFAULT '{}'
);

CREATE TABLE workspace_events (
    post_id BIGINT NOT NULL REFERENCES post_workspaces(post_id),
    id BIGINT NOT NULL,
    actor_id BIGINT REFERENCES users(id),
    kind TEXT NOT NULL,
    data JSONB NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (post_id, id)
);

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
    CHECK (octet_length(content) = size)
);
CREATE INDEX workspace_files_post ON workspace_files(post_id, created_at);

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

-- +goose Down

DROP TABLE IF EXISTS post_settlements;
DROP TABLE IF EXISTS workspace_files;
DROP TABLE IF EXISTS workspace_events;
DROP TABLE IF EXISTS post_workspaces;
DROP TABLE IF EXISTS post_escrows;
DROP TABLE IF EXISTS wallet_challenges;
DROP TABLE IF EXISTS wallets;
DROP TABLE IF EXISTS posts;
DROP TABLE IF EXISTS users;

DROP FUNCTION IF EXISTS review_workspace_event();
DROP FUNCTION IF EXISTS settlement_workspace_event();
DROP FUNCTION IF EXISTS escrow_workspace_event();
DROP FUNCTION IF EXISTS post_workspace_event();
DROP FUNCTION IF EXISTS workspace_event(BIGINT, BIGINT, TEXT, JSONB);

DROP TYPE IF EXISTS post_level;
DROP TYPE IF EXISTS post_status;
