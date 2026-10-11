package controller

import (
	"errors"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5"
	db "github.com/rajandhamala/Maruvo/db/sqlc"
)

type remoteView struct {
	Status            string     `json:"status"`
	AgentName         string     `json:"agent_name"`
	WorkerOnline      bool       `json:"worker_online"`
	WorkerSeen        *time.Time `json:"worker_seen,omitempty"`
	Detail            string     `json:"detail"`
	SubmissionVersion int64      `json:"submission_version"`
	LastEventID       int64      `json:"last_event_id"`
}

func remoteStatus(post db.Post, snapshot db.RemoteSnapshotsRow, now time.Time) remoteView {
	v := remoteView{AgentName: snapshot.AgentName, Detail: snapshot.Detail,
		SubmissionVersion: snapshot.SubmissionVersion, LastEventID: snapshot.LastEventID}
	executing := snapshot.ActiveUntil.Valid && snapshot.ActiveUntil.Time.After(now)

	v.WorkerOnline = executing || (snapshot.OfferUntil.Valid && snapshot.OfferUntil.Time.After(now))
	if snapshot.WorkerSeen.Valid {
		v.WorkerSeen = &snapshot.WorkerSeen.Time
	}

	switch {
	case post.Status == db.PostStatusCompleted || snapshot.EscrowState == "released":
		v.Status = "completed"
	case snapshot.EscrowState == "refunded":
		v.Status = "refunded"
	case post.Status == db.PostStatusCancelled:
		v.Status = "cancelled"
	case !post.AcceptedBy.Valid:
		v.Status = "queued"
	case snapshot.EscrowState != "confirmed":
		v.Status = "waiting_for_funding"
	case snapshot.ReviewState == "submitted":
		v.Status = "waiting_for_review"
	case snapshot.ActivityState == "waiting_for_answer":
		v.Status = "waiting_for_answer"
	case snapshot.ActivityState == "failed":
		v.Status = "failed"
	case snapshot.ActivityState == "waiting_for_review":
		v.Status = "waiting_for_worker"
	case snapshot.ActivityState == "interrupted" || (snapshot.WorkerSeen.Valid && !executing):
		v.Status = "interrupted"
	case snapshot.ActivityState == "working" || snapshot.ActivityState == "starting":
		v.Status = "working"
	default:
		v.Status = "waiting_for_worker"
	}

	return v
}

func (c *Controller) RemoteInbox(w http.ResponseWriter, r *http.Request) {
	userID, ok := postUserID(w, r)
	if !ok {
		return
	}

	posts, err := c.queries.RemoteInbox(r.Context(), userID)
	if err != nil {
		workspaceError(w, err)
		return
	}

	c.writePosts(w, r, posts)
}

func validActivity(state string) bool {
	switch state {
	case "", "starting", "working", "waiting_for_answer", "waiting_for_review", "interrupted", "failed":
		return true
	}

	return false
}

func (c *Controller) UpdateRemoteActivity(w http.ResponseWriter, r *http.Request) {
	var payload struct {
		RunID  string `json:"run_id"`
		State  string `json:"state"`
		Detail string `json:"detail"`
	}
	if !decodeOffer(w, r, &payload) {
		return
	}

	if len(payload.RunID) < 32 || len(payload.RunID) > 128 || !validActivity(payload.State) ||
		!validTaskText(payload.Detail, 1000, 4000) {
		postJSON(
			w,
			400,
			map[string]string{
				"error": "use a random run_id of 32-128 bytes, a valid activity state and detail up to 1000 characters",
			},
		)

		return
	}

	c.workspaceMutation(w, r, func(q *db.Queries, post db.Post, userID int64) (any, error) {
		if !post.AcceptedBy.Valid || post.AcceptedBy.Int64 != userID {
			return nil, &workspaceFailure{403, "only the assigned worker can report activity"}
		}

		control, err := agentControl(r.Context(), q, post.ID, userID)
		if err != nil {
			return nil, err
		}

		if control.Mode == "manual" && payload.State != "interrupted" && payload.State != "failed" {
			return nil, &workspaceFailure{
				409,
				"agent access is paused for this task; switch to agent mode to resume",
			}
		}

		previous, err := q.GetRemoteActivity(r.Context(), post.ID)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return nil, err
		}

		activity, err := q.UpdateRemoteActivity(r.Context(), db.UpdateRemoteActivityParams{
			PostID: post.ID, RunID: payload.RunID, State: payload.State, Detail: payload.Detail,
		})
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, &workspaceFailure{
				409,
				"another runner owns the live task; wait for its lease to expire",
			}
		}

		if err != nil {
			return nil, err
		}

		if payload.State != "" && (previous.State != activity.State || previous.Detail != activity.Detail) {
			if _, err = appendEvent(r, q, post.ID, userID, "agent.activity", map[string]string{
				"state": activity.State, "detail": activity.Detail,
			}); err != nil {
				return nil, err
			}
		}

		return map[string]any{"status": "reported", "activity": struct {
			State       string    `json:"state"`
			Detail      string    `json:"detail"`
			ActiveUntil time.Time `json:"active_until"`
		}{activity.State, activity.Detail, activity.ActiveUntil.Time}}, nil
	})
}
