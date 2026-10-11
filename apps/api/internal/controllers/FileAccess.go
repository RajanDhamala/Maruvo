package controller

import (
	"context"
	"errors"
	"net/http"
	"slices"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	db "github.com/rajandhamala/Maruvo/db/sqlc"
)

const refundBlockedMessage = "worker files have been accessed; unilateral refunds are blocked in Maruvo pending dispute resolution; payment approval and revision requests remain available"

func (c *Controller) workspaceDownload(ctx context.Context, r *http.Request, postID, userID int64, id pgtype.UUID) (db.WorkspaceFile, error) {
	var empty db.WorkspaceFile
	if err := c.checkSession(ctx, r); err != nil {
		return empty, err
	}
	tx, err := c.pool.Begin(ctx)
	if err != nil {
		return empty, err
	}
	defer tx.Rollback(ctx)
	q := db.New(tx)
	if err = lockAgent(ctx, q); err != nil {
		return empty, err
	}
	post, err := q.LockPost(ctx, postID)
	if errors.Is(err, pgx.ErrNoRows) {
		return empty, &workspaceFailure{404, "task not found"}
	}
	if err != nil {
		return empty, err
	}
	if !workspaceParticipant(post, userID) {
		reviewer, err := q.IsPostReviewer(ctx, db.IsPostReviewerParams{PostID: postID, UserID: userID})
		if err != nil {
			return empty, err
		}
		if !reviewer {
			return empty, &workspaceFailure{403, "workspace access denied"}
		}
	}
	file, err := q.GetWorkspaceFile(ctx, db.GetWorkspaceFileParams{ID: id, PostID: postID})
	if errors.Is(err, pgx.ErrNoRows) {
		return empty, &workspaceFailure{404, "file not found"}
	}
	if err != nil {
		return empty, err
	}
	if err = recordWorkspaceFileAccess(r.WithContext(ctx), q, post, file, userID); err != nil {
		return empty, err
	}
	// Commit access and the refund guard before any file bytes leave the server.
	if err = tx.Commit(ctx); err != nil {
		return empty, err
	}
	return file, nil
}

// The caller holds the post lock until its transaction commits. Settlement
// submission takes the same lock, so either access or a refund wins the race.
func recordWorkspaceFileAccess(r *http.Request, q *db.Queries, post db.Post, file db.WorkspaceFile, userID int64) error {
	ctx := r.Context()
	ws, err := q.GetWorkspace(ctx, post.ID)
	if err != nil {
		return err
	}
	protect := post.AcceptedBy.Valid && file.UploadedBy == post.AcceptedBy.Int64 &&
		userID != post.AcceptedBy.Int64 && (file.Purpose != "input" || slices.Contains(ws.DeliveryFiles, file.ID))
	if protect {
		settlement, err := q.GetPostSettlement(ctx, post.ID)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		if err == nil && settlement.Action == "refund" && (settlement.State == "pending" || settlement.State == "confirmed") {
			return &workspaceFailure{409, "worker file access is unavailable while a refund is pending or confirmed"}
		}
		escrow, err := q.GetPostEscrow(ctx, post.ID)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		if ws.ReviewState == "refunded" || (err == nil && escrow.State == "refunded") {
			return &workspaceFailure{409, "worker file access is unavailable after a refund"}
		}
		if err = q.BlockWorkspaceRefund(ctx, post.ID); err != nil {
			return err
		}
		if err = q.ExpirePreparedRefund(ctx, post.ID); err != nil {
			return err
		}
	}
	var grantID pgtype.UUID
	if agent := requestAgent(ctx); agent != nil {
		if err = grantID.Scan(agent.ID); err != nil {
			return err
		}
	}
	version := int64(0)
	if slices.Contains(ws.DeliveryFiles, file.ID) {
		version = ws.SubmissionVersion
	}
	receipt, err := q.RecordWorkspaceFileAccess(ctx, db.RecordWorkspaceFileAccessParams{
		PostID: post.ID, FileID: file.ID, UserID: userID, SubmissionVersion: version,
		AgentGrantID: grantID, Sha256: file.Sha256, Size: file.Size, ProtectsRefund: protect,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil // A lost-response retry retains the original receipt and event.
	}
	if err != nil {
		return err
	}
	_, err = appendEvent(r, q, post.ID, userID, "file.accessed", map[string]any{
		"id": file.ID, "name": file.Name, "sha256": file.Sha256, "size": file.Size,
		"submission_version": version, "accessed_at": receipt.AccessedAt,
		"refund_blocked": ws.RefundBlocked || protect,
	})
	return err
}
