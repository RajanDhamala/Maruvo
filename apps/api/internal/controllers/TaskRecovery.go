package controller

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	db "github.com/rajandhamala/Maruvo/db/sqlc"
	"github.com/rajandhamala/Maruvo/pb"
)

func (c *Controller) RecoverPost(w http.ResponseWriter, r *http.Request) {
	userID, ok := postUserID(w, r)
	if !ok {
		return
	}

	var payload struct {
		ID        int64     `json:"id"`
		Action    string    `json:"action"`
		EndTime   time.Time `json:"end_time"`
		DeliverBy time.Time `json:"deliver_by"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&payload) != nil ||
		payload.ID <= 0 || (payload.Action != "cancel" && payload.Action != "reopen") {
		postJSON(w, 400, map[string]string{"error": "provide a task ID and cancel or reopen action"})
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()

	tx, err := c.pool.Begin(ctx)
	if err != nil {
		workspaceError(w, err)
		return
	}
	defer tx.Rollback(ctx)

	q := db.New(tx)

	post, err := q.LockPost(ctx, payload.ID)
	if errors.Is(err, pgx.ErrNoRows) {
		postJSON(w, 404, map[string]string{"error": "task not found"})
		return
	}

	if err != nil {
		workspaceError(w, err)
		return
	}

	if post.UserID != userID {
		postJSON(w, 403, map[string]string{"error": "only the requester can cancel or reopen this task"})
		return
	}

	if !post.AcceptedBy.Valid ||
		(post.Status != db.PostStatusNegotiating && post.Status != db.PostStatusCancelled) {
		postJSON(w, 409, map[string]string{"error": "recovery requires an accepted, unfunded task"})
		return
	}

	if payload.Action == "reopen" && post.ReopenedAs.Valid {
		reopened, err := q.GetPost(ctx, post.ReopenedAs.Int64)
		if err != nil {
			workspaceError(w, err)
			return
		}

		if err = tx.Commit(ctx); err != nil {
			workspaceError(w, err)
			return
		}

		c.writePost(w, r, 200, reopened, map[string]any{"reopened_from": post.ID})

		return
	}

	if err = c.checkRecoveryFunding(ctx, q, post); err != nil {
		workspaceError(w, err)
		return
	}

	deadline := payload.EndTime
	if deadline.IsZero() {
		deadline = post.EndTime.Time
	}

	deadline = deadline.UTC().Truncate(time.Microsecond)

	if payload.Action == "reopen" && !deadline.After(time.Now()) {
		postJSON(w, 400, map[string]string{"error": "choose a future acceptance cutoff to reopen this task"})
		return
	}

	deliverBy := post.DeliverBy
	if !payload.DeliverBy.IsZero() {
		deliverBy = pgtype.Timestamptz{Time: payload.DeliverBy.UTC().Truncate(time.Microsecond), Valid: true}
	}

	if payload.Action == "reopen" {
		if err := validateTaskTiming(
			deadline,
			deliverBy.Time,
			post.FundingWindowSeconds,
			post.ReviewWindowSeconds,
		); err != nil {
			postJSON(w, 400, map[string]string{"error": err.Error()})
			return
		}
	}

	if post.Status == db.PostStatusNegotiating {
		post, err = q.CancelUnfundedPost(ctx, post.ID)
		if err != nil {
			workspaceError(w, err)
			return
		}
	}

	result := post
	fields := map[string]any{}

	if payload.Action == "reopen" {
		result, err = q.CreatePost(ctx, db.CreatePostParams{
			TargetWorker: post.TargetWorker,
			UserID:       post.UserID,
			Title:        post.Title,
			CostLamports: post.CostLamports,
			EndTime: pgtype.Timestamptz{
				Time:  deadline,
				Valid: true,
			},
			Status:               db.PostStatusOpen,
			Level:                post.Level,
			Description:          post.Description,
			AcceptanceCriteria:   post.AcceptanceCriteria,
			InputFiles:           post.InputFiles,
			ExpectedOutputs:      post.ExpectedOutputs,
			FundingWindowSeconds: post.FundingWindowSeconds,
			DeliverBy:            deliverBy,
			ReviewWindowSeconds:  post.ReviewWindowSeconds,
		})
		if err != nil {
			workspaceError(w, err)
			return
		}

		if _, err = q.LinkReopenedPost(ctx, db.LinkReopenedPostParams{ID: post.ID,
			ReopenedAs: pgtype.Int8{Int64: result.ID, Valid: true}}); err != nil {
			workspaceError(w, err)
			return
		}

		data, _ := json.Marshal(map[string]int64{"post_id": result.ID})
		if _, err = q.AppendWorkspaceEvent(ctx, db.AppendWorkspaceEventParams{
			PostID:  post.ID,
			ActorID: pgtype.Int8{Int64: userID, Valid: true},
			Kind:    "task.reopened",
			Data:    data,
		}); err != nil {
			workspaceError(w, err)
			return
		}

		fields["reopened_from"] = post.ID
	}

	if err = tx.Commit(ctx); err != nil {
		workspaceError(w, err)
		return
	}

	c.writePost(w, r, 200, result, fields)
}

func (c *Controller) checkRecoveryFunding(ctx context.Context, q *db.Queries, post db.Post) error {
	escrow, err := q.GetPostEscrow(ctx, post.ID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}

	if err != nil {
		return err
	}

	request := checkRequest(post, escrow)

	result, err := pb.NewSolanaServiceClient(c.rpc).CheckEscrowRecovery(ctx, request)
	if err != nil {
		return err
	}

	if result.State != "expired" {
		return &workspaceFailure{
			code:    409,
			message: "funding is active or already confirmed; wait for expiry or use the reviewer refund flow",
		}
	}

	return q.UpdateEscrowState(ctx, db.UpdateEscrowStateParams{PostID: post.ID, State: "expired"})
}
