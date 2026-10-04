package controller

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	db "github.com/rajandhamala/Maruvo/db/sqlc"
	"github.com/rajandhamala/Maruvo/internal/utils"
	"github.com/rajandhamala/Maruvo/pb"
)

type settlementView struct {
	State             string `json:"state"`
	Action            string `json:"action"`
	Note              string `json:"note"`
	SubmissionVersion int64  `json:"submission_version"`
	Signature         string `json:"signature"`
	FeeLamports       int64  `json:"fee_lamports"`
	Transaction       string `json:"transaction,omitempty"`
}

func viewSettlement(saved db.PostSettlement, signing bool) settlementView {
	view := settlementView{
		State:             saved.State,
		Action:            saved.Action,
		Note:              saved.Note,
		SubmissionVersion: saved.SubmissionVersion,
		Signature:         saved.Signature,
		FeeLamports:       saved.FeeLamports,
	}
	if signing && saved.State == "prepared" {
		view.Transaction = saved.Transaction
	}

	return view
}

func checkRequest(post db.Post, escrow db.PostEscrow) *pb.CheckEscrowRequest {
	return &pb.CheckEscrowRequest{
		Agreement:            agreement(post, escrow.AgreementVersion),
		Address:              escrow.Address,
		Signature:            escrow.Signature,
		LastValidBlockHeight: uint64(escrow.LastValidBlockHeight),
		ProgramId:            escrow.ProgramID,
		Reviewer:             escrow.Reviewer,
		Network:              escrow.Network,
	}
}

func requireReviewer(w http.ResponseWriter, r *http.Request, q *db.Queries, postID, userID int64) bool {
	allowed, err := q.IsPostReviewer(r.Context(), db.IsPostReviewerParams{PostID: postID, UserID: userID})
	if err != nil {
		workspaceError(w, err)
		return false
	}

	if !allowed {
		postJSON(
			w,
			403,
			map[string]string{"error": "only the account linked to this escrow's reviewer wallet can review"},
		)

		return false
	}

	return true
}

type reviewPayload struct {
	Action            string `json:"action"`
	Note              string `json:"note"`
	SubmissionVersion int64  `json:"submission_version"`
}

func readReview(w http.ResponseWriter, r *http.Request) (reviewPayload, bool) {
	payload := reviewPayload{SubmissionVersion: -1}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 20<<10)).Decode(&payload) != nil {
		postJSON(w, 400, map[string]string{"error": "invalid review payload"})
		return payload, false
	}

	payload.Note = strings.TrimSpace(payload.Note)
	if payload.Note == "" || !utf8.ValidString(payload.Note) || len([]rune(payload.Note)) > 4000 ||
		payload.SubmissionVersion < 0 {
		postJSON(
			w,
			400,
			map[string]string{"error": "provide a review note and the current submission version"},
		)

		return payload, false
	}

	return payload, true
}

func canSettle(workspace db.PostWorkspace, action string, version int64) bool {
	if workspace.SubmissionVersion != version || version < 0 {
		return false
	}

	if action == "release" {
		return version > 0 && workspace.ReviewState == "submitted"
	}

	return action == "refund" && (workspace.ReviewState == "working" ||
		workspace.ReviewState == "submitted" || workspace.ReviewState == "changes_requested")
}

func (c *Controller) PreparePostSettlement(w http.ResponseWriter, r *http.Request) {
	payload, ok := readReview(w, r)
	if !ok {
		return
	}

	if payload.Action != "release" && payload.Action != "refund" {
		postJSON(w, 400, map[string]string{"error": "choose release or refund"})
		return
	}

	c.workspaceMutation(w, r, func(q *db.Queries, post db.Post, userID int64) (any, error) {
		if !requireReviewer(w, r, q, post.ID, userID) {
			return nil, nil
		}

		escrow, err := q.GetPostEscrow(r.Context(), post.ID)
		if err != nil {
			return nil, err
		}

		post, escrow, err = c.syncEscrow(r.Context(), q, post, escrow, false)
		if err != nil {
			return nil, err
		}

		if escrow.State != "confirmed" {
			postJSON(w, 409, map[string]string{"error": "escrow is not funded or has already settled"})
			return nil, nil
		}

		workspace, err := q.GetWorkspace(r.Context(), post.ID)
		if err != nil {
			return nil, err
		}

		if !canSettle(workspace, payload.Action, payload.SubmissionVersion) {
			postJSON(
				w,
				409,
				map[string]string{"error": "refresh the current delivery; payout requires submitted work"},
			)

			return nil, nil
		}

		saved, err := q.GetPostSettlement(r.Context(), post.ID)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return nil, err
		}

		if err == nil {
			saved, err = c.syncSettlement(r.Context(), q, post, escrow, saved, false)
			if err != nil {
				return nil, err
			}

			if saved.State != "failed" && saved.State != "expired" {
				if saved.Action != payload.Action || saved.SubmissionVersion != payload.SubmissionVersion {
					postJSON(
						w,
						409,
						map[string]string{
							"error": "another settlement is active; wait for confirmation or expiry",
						},
					)

					return nil, nil
				}

				return map[string]any{
					"settlement": viewSettlement(saved, true),
					"escrow":     escrowView(escrow, false),
				}, nil
			}
		}

		plan, err := pb.NewSolanaServiceClient(c.rpc).
			PrepareSettlement(r.Context(), &pb.PrepareSettlementRequest{Escrow: checkRequest(post, escrow), Action: payload.Action})
		if err != nil {
			return nil, err
		}

		saved, err = q.SavePostSettlement(
			r.Context(),
			db.SavePostSettlementParams{
				PostID:               post.ID,
				ReviewerID:           userID,
				Action:               payload.Action,
				SubmissionVersion:    payload.SubmissionVersion,
				Note:                 payload.Note,
				Transaction:          plan.Transaction,
				LastValidBlockHeight: int64(plan.LastValidBlockHeight),
				FeeLamports:          int64(plan.FeeLamports),
			},
		)

		return map[string]any{
			"settlement": viewSettlement(saved, true),
			"escrow":     escrowView(escrow, false),
		}, err
	})
}

func (c *Controller) RequestPostChanges(w http.ResponseWriter, r *http.Request) {
	payload, ok := readReview(w, r)
	if !ok {
		return
	}

	c.workspaceMutation(w, r, func(q *db.Queries, post db.Post, userID int64) (any, error) {
		if !requireReviewer(w, r, q, post.ID, userID) {
			return nil, nil
		}

		workspace, err := q.GetWorkspace(r.Context(), post.ID)
		if err != nil {
			return nil, err
		}

		if workspace.ReviewState != "submitted" || workspace.SubmissionVersion != payload.SubmissionVersion {
			postJSON(w, 409, map[string]string{"error": "refresh and review the current submission"})
			return nil, nil
		}

		escrow, err := q.GetPostEscrow(r.Context(), post.ID)
		if err != nil {
			return nil, err
		}

		post, escrow, err = c.syncEscrow(r.Context(), q, post, escrow, false)
		if err != nil {
			return nil, err
		}

		if escrow.State != "confirmed" {
			postJSON(w, 409, map[string]string{"error": "escrow has already settled or is not funded"})
			return nil, nil
		}

		saved, err := q.GetPostSettlement(r.Context(), post.ID)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return nil, err
		}

		if err == nil {
			saved, err = c.syncSettlement(r.Context(), q, post, escrow, saved, false)
			if err != nil {
				return nil, err
			}

			if saved.State != "failed" && saved.State != "expired" {
				postJSON(
					w,
					409,
					map[string]string{
						"error": "settlement already prepared; wait for confirmation or expiry",
					},
				)

				return nil, nil
			}
		}

		_, err = q.RequestWorkspaceChanges(
			r.Context(),
			db.RequestWorkspaceChangesParams{PostID: post.ID, ReviewNote: payload.Note},
		)
		if err != nil {
			return nil, err
		}

		id, err := appendEvent(
			r,
			q,
			post.ID,
			userID,
			"review.changes_requested",
			map[string]any{"note": payload.Note, "submission_version": payload.SubmissionVersion},
		)

		return map[string]any{
			"event_id":           id,
			"post_id":            post.ID,
			"submission_version": payload.SubmissionVersion,
			"review_state":       "changes_requested",
		}, err
	})
}

func (c *Controller) SubmitPostSettlement(w http.ResponseWriter, r *http.Request) {
	userID, ok := postUserID(w, r)
	if !ok {
		return
	}

	id, ok := workspacePostID(w, r)
	if !ok {
		return
	}

	var payload struct {
		Transaction string `json:"transaction"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&payload) != nil ||
		payload.Transaction == "" {
		postJSON(w, 400, map[string]string{"error": "invalid signed settlement"})
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()

	tx, err := c.pool.Begin(ctx)
	if err != nil {
		escrowError(w, err)
		return
	}
	defer tx.Rollback(ctx)

	q := db.New(tx)

	post, err := q.LockPost(ctx, id)
	if !workspaceAccess(w, r, q, post, err, userID) || !requireReviewer(w, r, q, id, userID) {
		return
	}

	escrow, err := q.GetPostEscrow(ctx, id)
	if err != nil {
		escrowError(w, err)
		return
	}

	saved, err := q.GetPostSettlement(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		postJSON(w, 409, map[string]string{"error": "prepare settlement first"})
		return
	}

	if err != nil {
		escrowError(w, err)
		return
	}

	signature, err := utils.TransactionSignature(saved.Transaction, payload.Transaction, escrow.Reviewer)
	if err != nil {
		postJSON(w, 400, map[string]string{"error": err.Error()})
		return
	}

	if saved.State == "confirmed" && saved.Signature == signature {
		postJSON(w, 200, map[string]any{"settlement": viewSettlement(saved, false)})
		return
	}

	workspace, err := q.GetWorkspace(ctx, id)
	if err != nil {
		escrowError(w, err)
		return
	}

	if !canSettle(workspace, saved.Action, saved.SubmissionVersion) ||
		escrow.State != "confirmed" {
		postJSON(w, 409, map[string]string{"error": "delivery or escrow changed; refresh before settling"})
		return
	}

	if saved.State == "prepared" {
		saved, err = q.MarkSettlementSubmitted(
			ctx,
			db.MarkSettlementSubmittedParams{
				PostID:            id,
				Signature:         signature,
				SignedTransaction: payload.Transaction,
			},
		)
		if err != nil {
			escrowError(w, err)
			return
		}
	} else if saved.State != "pending" || saved.Signature != signature || saved.SignedTransaction != payload.Transaction {
		postJSON(
			w,
			409,
			map[string]string{"error": "settlement already submitted, failed, or expired; refresh"},
		)

		return
	}
	// Save the signed transaction before RPC so the monitor can retry after a timeout or restart.
	if err = tx.Commit(ctx); err != nil {
		escrowError(w, err)
		return
	}

	_, err = pb.NewSolanaServiceClient(c.rpc).
		SubmitEscrow(ctx, &pb.SubmitEscrowRequest{Transaction: saved.Transaction, SignedTransaction: payload.Transaction})
	if err != nil {
		escrowError(w, err)
		return
	}

	postJSON(
		w,
		202,
		map[string]any{
			"settlement": viewSettlement(saved, false),
			"message":    "Settlement submitted. Waiting for on-chain confirmation.",
		},
	)
}

func (c *Controller) syncSettlement(
	ctx context.Context,
	q *db.Queries,
	post db.Post,
	escrow db.PostEscrow,
	saved db.PostSettlement,
	retry bool,
) (db.PostSettlement, error) {
	if saved.State != "prepared" && saved.State != "pending" {
		return saved, nil
	}

	client := pb.NewSolanaServiceClient(c.rpc)

	result, err := client.CheckSettlement(
		ctx,
		&pb.CheckSettlementRequest{
			Escrow:               checkRequest(post, escrow),
			Action:               saved.Action,
			Signature:            saved.Signature,
			LastValidBlockHeight: uint64(saved.LastValidBlockHeight),
		},
	)
	if err != nil {
		return saved, err
	}

	switch result.State {
	case "prepared", "pending", "confirmed", "failed", "expired":
	default:
		return saved, errors.New("unknown settlement state")
	}

	if result.State != saved.State {
		if err = q.UpdateSettlementState(
			ctx,
			db.UpdateSettlementStateParams{PostID: post.ID, State: result.State},
		); err != nil {
			return saved, err
		}

		saved.State = result.State
	}

	if retry && result.State == "pending" && saved.SignedTransaction != "" {
		_, _ = client.SubmitEscrow(
			ctx,
			&pb.SubmitEscrowRequest{
				Transaction:       saved.Transaction,
				SignedTransaction: saved.SignedTransaction,
			},
		)
	}

	return saved, nil
}
