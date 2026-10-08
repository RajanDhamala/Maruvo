package controller

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5"

	db "github.com/rajandhamala/Maruvo/db/sqlc"
)

type agentControlView struct {
	PostID        int64      `json:"post_id"`
	OwnerID       int64      `json:"owner_id"`
	Mode          string     `json:"mode"`
	UpdatedAt     *time.Time `json:"updated_at,omitempty"`
	RevokedGrants int64      `json:"revoked_grants"`
}

func agentControl(ctx context.Context, q *db.Queries, postID, ownerID int64) (agentControlView, error) {
	v := agentControlView{PostID: postID, OwnerID: ownerID, Mode: "agent"}

	control, err := q.GetAgentControl(ctx, db.GetAgentControlParams{PostID: postID, OwnerID: ownerID})
	if errors.Is(err, pgx.ErrNoRows) {
		return v, nil
	}

	if err != nil {
		return v, err
	}

	v.Mode, v.UpdatedAt = control.Mode, &control.UpdatedAt.Time

	return v, nil
}

func lockAgentControl(
	ctx context.Context,
	q *db.Queries,
	postID, ownerID int64,
) (db.PostAgentControl, error) {
	if err := q.EnsureAgentControl(
		ctx,
		db.EnsureAgentControlParams{PostID: postID, OwnerID: ownerID},
	); err != nil {
		return db.PostAgentControl{}, err
	}

	return q.LockAgentControl(ctx, db.LockAgentControlParams{PostID: postID, OwnerID: ownerID})
}

func (c *Controller) GetAgentControl(w http.ResponseWriter, r *http.Request) {
	ownerID, ok := postUserID(w, r)
	if !ok {
		return
	}

	postID, ok := workspacePostID(w, r)
	if !ok {
		return
	}

	post, err := c.queries.GetPost(r.Context(), postID)
	if !workspaceAccess(w, r, c.queries, post, err, ownerID) {
		return
	}

	control, err := agentControl(r.Context(), c.queries, postID, ownerID)
	if err != nil {
		workspaceError(w, err)
		return
	}

	w.Header().Set("Cache-Control", "no-store")
	postJSON(w, 200, control)
}

func (c *Controller) SetAgentControl(w http.ResponseWriter, r *http.Request) {
	var payload struct {
		Mode string `json:"mode"`
	}
	if !decodeOffer(w, r, &payload) {
		return
	}

	if payload.Mode != "manual" && payload.Mode != "agent" {
		postJSON(w, 400, map[string]string{"error": "mode must be manual or agent"})
		return
	}

	ownerID, ok := postUserID(w, r)
	if !ok {
		return
	}

	postID, ok := workspacePostID(w, r)
	if !ok {
		return
	}

	tx, err := c.pool.Begin(r.Context())
	if err != nil {
		workspaceError(w, err)
		return
	}
	defer tx.Rollback(r.Context())

	q := db.New(tx)

	post, err := q.GetPost(r.Context(), postID)
	if !workspaceAccess(w, r, q, post, err, ownerID) {
		return
	}

	previous, err := lockAgentControl(r.Context(), q, postID, ownerID)
	if err != nil {
		workspaceError(w, err)
		return
	}

	control, err := q.SetAgentControl(r.Context(), db.SetAgentControlParams{
		PostID: postID, OwnerID: ownerID, Mode: payload.Mode,
	})
	if err != nil {
		workspaceError(w, err)
		return
	}

	v := agentControlView{
		PostID:    postID,
		OwnerID:   ownerID,
		Mode:      control.Mode,
		UpdatedAt: &control.UpdatedAt.Time,
	}
	if payload.Mode == "manual" {
		v.RevokedGrants, err = q.RevokeTaskAgentGrants(r.Context(), db.RevokeTaskAgentGrantsParams{
			PostID: postID, OwnerID: ownerID,
		})
		if err != nil {
			workspaceError(w, err)
			return
		}
	}

	if previous.Mode != control.Mode || v.RevokedGrants > 0 {
		if _, err = appendEvent(r, q, postID, ownerID, "agent.control", v); err != nil {
			workspaceError(w, err)
			return
		}
	}

	if err = tx.Commit(r.Context()); err != nil {
		workspaceError(w, err)
		return
	}

	w.Header().Set("Cache-Control", "no-store")
	postJSON(w, 200, v)
}
