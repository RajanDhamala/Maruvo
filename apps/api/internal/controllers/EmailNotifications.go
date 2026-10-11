package controller

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/rajandhamala/Maruvo/internal/utils"
)

type emailSender interface {
	Send(context.Context, string, string, string, string) error
}

func (c *Controller) RunEmailWorker(ctx context.Context, sender emailSender) {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for ctx.Err() == nil {
		// Bound each sweep so shutdown and other workers can make progress.
		for i := 0; i < 100 && ctx.Err() == nil; i++ {
			found, err := c.sendNextEmail(ctx, sender)
			if err != nil {
				if ctx.Err() == nil {
					log.Printf("email worker: %v", err)
				}
				break
			}
			if !found {
				break
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (c *Controller) sendNextEmail(ctx context.Context, sender emailSender) (bool, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	tx, err := c.pool.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer tx.Rollback(context.Background())
	var id int64
	var to, subject, body string
	err = tx.QueryRow(ctx, `SELECT n.id, n.recipient, n.subject, n.body
 FROM email_notifications n LEFT JOIN email_preferences p ON p.user_id = n.recipient_id
 WHERE n.sent_at IS NULL AND n.attempts < 10 AND n.next_attempt_at <= NOW()
 AND COALESCE(p.enabled, true)
 ORDER BY n.next_attempt_at, n.id FOR UPDATE OF n SKIP LOCKED LIMIT 1`).Scan(&id, &to, &subject, &body)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	sendErr := sender.Send(ctx, to, subject, body, fmt.Sprintf("maruvo-email-%d", id))
	if sendErr == nil {
		_, err = tx.Exec(ctx, `UPDATE email_notifications SET sent_at = NOW(), attempts = attempts + 1 WHERE id = $1`, id)
	} else {
		// Never store provider errors: they may contain addresses or credentials.
		log.Printf("email notification %d failed; retry queued", id)
		_, err = tx.Exec(ctx, `UPDATE email_notifications SET attempts = attempts + 1,
   next_attempt_at = NOW() + LEAST(3600, 30 * power(2, attempts)) * interval '1 second' WHERE id = $1`, id)
	}
	if err != nil {
		return true, err
	}
	return true, tx.Commit(ctx)
}

func (c *Controller) EmailPreferences(w http.ResponseWriter, r *http.Request) {
	if r.Context().Value(utils.AgentKey) != nil {
		postJSON(w, 403, map[string]string{"error": "email preferences require an account login"})
		return
	}
	id, ok := postUserID(w, r)
	if !ok {
		return
	}
	enabled := true
	if r.Method == http.MethodPut {
		var payload struct {
			Enabled *bool `json:"enabled"`
		}
		if json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024)).Decode(&payload) != nil || payload.Enabled == nil {
			postJSON(w, 400, map[string]string{"error": "provide enabled as a boolean"})
			return
		}
		enabled = *payload.Enabled
		_, err := c.pool.Exec(r.Context(), `INSERT INTO email_preferences(user_id, enabled) VALUES ($1, $2)
   ON CONFLICT (user_id) DO UPDATE SET enabled = EXCLUDED.enabled`, id, enabled)
		if err != nil {
			workspaceError(w, err)
			return
		}
	}
	var hasEmail bool
	err := c.pool.QueryRow(r.Context(), `SELECT COALESCE(p.enabled, true), COALESCE(u.email <> '', false)
 FROM users u LEFT JOIN email_preferences p ON p.user_id = u.id WHERE u.id = $1`, id).Scan(&enabled, &hasEmail)
	if errors.Is(err, pgx.ErrNoRows) {
		postJSON(w, 401, map[string]string{"error": "account not found"})
		return
	}
	if err != nil {
		workspaceError(w, err)
		return
	}
	postJSON(w, 200, map[string]bool{"enabled": enabled, "has_email": hasEmail})
}
