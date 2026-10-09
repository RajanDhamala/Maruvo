-- +goose Up
CREATE TABLE email_preferences (
    user_id BIGINT PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    enabled BOOLEAN NOT NULL DEFAULT true
);

CREATE TABLE email_notifications (
    id BIGSERIAL PRIMARY KEY,
    post_id BIGINT NOT NULL,
    event_id BIGINT NOT NULL,
    recipient_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    recipient TEXT NOT NULL,
    subject TEXT NOT NULL,
    body TEXT NOT NULL,
    attempts INTEGER NOT NULL DEFAULT 0,
    next_attempt_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    sent_at TIMESTAMPTZ,
    UNIQUE (post_id, event_id, recipient_id),
    FOREIGN KEY (post_id, event_id) REFERENCES workspace_events(post_id, id) ON DELETE CASCADE
);
CREATE INDEX email_notifications_pending ON email_notifications(next_attempt_at, id)
    WHERE sent_at IS NULL AND attempts < 10;

-- +goose StatementBegin
CREATE FUNCTION queue_job_email() RETURNS TRIGGER LANGUAGE plpgsql AS $$
DECLARE
    summary TEXT;
    job posts%ROWTYPE;
BEGIN
    summary := CASE
        WHEN NEW.kind = 'task.accepted' THEN 'Job accepted'
        WHEN NEW.kind = 'escrow.updated' AND NEW.data->>'state' = 'confirmed' THEN 'Funding confirmed'
        WHEN NEW.kind = 'work.submitted' THEN 'Work submitted for review'
        WHEN NEW.kind = 'review.changes_requested' THEN 'Changes requested'
        WHEN NEW.kind = 'review.completed' AND NEW.data->>'review_state' = 'approved' THEN 'Payment released'
        WHEN NEW.kind = 'review.completed' AND NEW.data->>'review_state' = 'refunded' THEN 'Payment refunded'
        WHEN NEW.kind = 'task.status' AND NEW.data->>'status' = 'cancelled' THEN 'Job cancelled'
        WHEN NEW.kind = 'task.reopened' THEN 'Job reopened'
        WHEN NEW.kind = 'task.overdue' THEN 'Deadline overdue'
        ELSE NULL
    END;
    IF summary IS NULL THEN RETURN NEW; END IF;
    SELECT * INTO job FROM posts WHERE id = NEW.post_id;
    INSERT INTO email_notifications(post_id, event_id, recipient_id, recipient, subject, body)
    SELECT NEW.post_id, NEW.id, u.id, u.email, 'Maruvo | ' || summary || ' | Job #' || job.id,
        E'Maruvo\n\n' || summary || E'\n\nJob: ' || job.title || E'\nID: #' || job.id ||
        E'\n\nOpen the job in Maruvo for details and next steps.'
    FROM users u LEFT JOIN email_preferences pref ON pref.user_id = u.id
    WHERE u.email IS NOT NULL AND btrim(u.email) <> '' AND COALESCE(pref.enabled, true)
        AND (NEW.actor_id IS NULL OR u.id <> NEW.actor_id)
        AND (u.id = job.user_id OR u.id = job.accepted_by OR
            (NEW.kind IN ('work.submitted', 'task.overdue') AND EXISTS (
                SELECT 1 FROM post_escrows e JOIN wallets w ON w.address = e.reviewer
                WHERE e.post_id = job.id AND w.user_id = u.id)))
    ON CONFLICT DO NOTHING;
    RETURN NEW;
END;
$$;
-- +goose StatementEnd
CREATE TRIGGER queue_job_email AFTER INSERT ON workspace_events
    FOR EACH ROW EXECUTE FUNCTION queue_job_email();

-- +goose Down
DROP TRIGGER queue_job_email ON workspace_events;
DROP FUNCTION queue_job_email();
DROP TABLE email_notifications;
DROP TABLE email_preferences;
