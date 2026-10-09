package controller

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

type testEmailSender struct {
	fail  bool
	calls int
	to    string
}

func (s *testEmailSender) Send(_ context.Context, to, _, _, _ string) error {
	s.calls++
	s.to = to
	if s.fail {
		return errors.New("provider unavailable")
	}
	return nil
}

func TestEmailNotificationsLifecycle(t *testing.T) {
	url := os.Getenv("EMAIL_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("set EMAIL_TEST_DATABASE_URL to an isolated migrated database")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(ctx, sql, args...); err != nil {
			t.Fatal(err)
		}
	}
	// This test deliberately requires a disposable database.
	exec(`TRUNCATE users RESTART IDENTITY CASCADE`)
	exec(`INSERT INTO users(email,username) VALUES ('owner@example.com','owner'), ('worker@example.com','worker'), (NULL,'no-email')`)
	exec(`INSERT INTO posts(user_id,title,cost_lamports,end_time,level) VALUES(1,'Test job',100,NOW()+interval '1 day','easy')`)
	exec(`INSERT INTO post_workspaces(post_id) VALUES(1)`)
	exec(`UPDATE posts SET accepted_by=2, accepted_at=NOW(), status='negotiating' WHERE id=1`)
	var count int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM email_notifications WHERE recipient='owner@example.com'`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("acceptance owner notifications: %d, %v", count, err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM email_notifications WHERE recipient_id=2`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("actor notified: %d, %v", count, err)
	}
	c := &Controller{pool: pool}
	sender := &testEmailSender{fail: true}
	if found, err := c.sendNextEmail(ctx, sender); err != nil || !found {
		t.Fatalf("failure attempt: %v %v", found, err)
	}
	if found, err := c.sendNextEmail(ctx, sender); err != nil || found {
		t.Fatalf("retry must wait: %v %v", found, err)
	}
	var attempts int
	if err := pool.QueryRow(ctx, `SELECT attempts FROM email_notifications LIMIT 1`).Scan(&attempts); err != nil || attempts != 1 {
		t.Fatalf("attempts: %d %v", attempts, err)
	}
	exec(`UPDATE email_notifications SET next_attempt_at=NOW()`)
	exec(`INSERT INTO email_preferences(user_id,enabled) VALUES(1,false)`)
	if found, err := c.sendNextEmail(ctx, sender); err != nil || found {
		t.Fatalf("opt-out: %v %v", found, err)
	}
	exec(`UPDATE email_preferences SET enabled=true WHERE user_id=1`)
	sender.fail = false
	if found, err := c.sendNextEmail(ctx, sender); err != nil || !found || sender.to != "owner@example.com" {
		t.Fatalf("delivery: %v %v %s", found, err, sender.to)
	}
	if found, err := c.sendNextEmail(ctx, sender); err != nil || found {
		t.Fatalf("resent delivered email: %v %v", found, err)
	}
	exec(`SELECT workspace_event(1,2,'message','{}'::jsonb)`)
	exec(`SELECT workspace_event(1,2,'work.submitted','{}'::jsonb)`)
	exec(`SELECT workspace_event(1,1,'review.changes_requested','{}'::jsonb)`)
	exec(`SELECT workspace_event(1,NULL,'escrow.updated','{"state":"pending"}'::jsonb)`)
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM email_notifications`).Scan(&count); err != nil || count != 3 {
		t.Fatalf("event filtering: %d %v", count, err)
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `SELECT workspace_event(1,NULL,'task.overdue','{}'::jsonb)`); err != nil {
		t.Fatal(err)
	}
	if err := tx.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM email_notifications`).Scan(&count); err != nil || count != 3 {
		t.Fatalf("rollback queued email: %d %v", count, err)
	}
}
