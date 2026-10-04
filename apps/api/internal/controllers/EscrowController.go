package controller

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	db "github.com/rajandhamala/Maruvo/db/sqlc"
	"github.com/rajandhamala/Maruvo/internal/utils"
	"github.com/rajandhamala/Maruvo/pb"
	"google.golang.org/grpc/status"
)

type fundingView struct {
	AgreementVersion int32  `json:"agreement_version"`
	State            string `json:"state"`
	Address          string `json:"address"`
	ProgramID        string `json:"program_id"`
	Reviewer         string `json:"reviewer"`
	Network          string `json:"network"`
	Signature        string `json:"signature"`
	Transaction      string `json:"transaction,omitempty"`
	FeeLamports      int64  `json:"fee_lamports"`
	StorageLamports  int64  `json:"storage_lamports"`
}

func escrowView(escrow db.PostEscrow, signing bool) fundingView {
	view := fundingView{
		AgreementVersion: escrow.AgreementVersion,
		State:            escrow.State,
		Address:          escrow.Address,
		ProgramID:        escrow.ProgramID,
		Reviewer:         escrow.Reviewer,
		Network:          escrow.Network,
		Signature:        escrow.Signature,
		FeeLamports:      escrow.FeeLamports,
		StorageLamports:  escrow.StorageLamports,
	}
	if signing && escrow.State == "prepared" {
		view.Transaction = escrow.Transaction
	}

	return view
}

func agreement(post db.Post, version int32) *pb.PrepareEscrowRequest {
	terms := fmt.Sprintf(
		"maruvo-escrow-v1\n%d\n%d\n%d\n%s\n%s\n%d\n%s\n%s\n%s",
		post.ID,
		post.UserID,
		post.AcceptedBy.Int64,
		post.PosterWallet,
		post.WorkerWallet,
		post.CostLamports,
		post.EndTime.Time.UTC().Format(time.RFC3339Nano),
		post.Level,
		post.Title,
	)
	if version == 2 || version == 3 {
		values := []any{
			"maruvo-escrow-v2", post.ID, post.UserID, post.AcceptedBy.Int64,
			post.PosterWallet, post.WorkerWallet, post.CostLamports,
			post.EndTime.Time.UTC().Format(time.RFC3339Nano), string(post.Level), post.Title,
			post.Description, post.AcceptanceCriteria,
			append([]string{}, post.InputFiles...), append([]string{}, post.ExpectedOutputs...),
		}
		if version == 3 {
			values[0] = "maruvo-escrow-v3"
			values = append(values, post.FundingWindowSeconds,
				post.FundBy.Time.UTC().Format(time.RFC3339Nano),
				post.DeliverBy.Time.UTC().Format(time.RFC3339Nano), post.ReviewWindowSeconds)
		}

		data, _ := json.Marshal(values)
		terms = string(data)
	}

	hash := sha256.Sum256([]byte(terms))

	return &pb.PrepareEscrowRequest{
		PostId:        uint64(post.ID),
		Lamports:      uint64(post.CostLamports),
		Poster:        post.PosterWallet,
		Worker:        post.WorkerWallet,
		AgreementHash: hash[:],
	}
}

func postIDPayload(w http.ResponseWriter, r *http.Request) (int64, bool) {
	var payload DeletePostPayload
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&payload) != nil || payload.ID <= 0 {
		postJSON(w, 400, map[string]string{"error": "invalid post ID"})
		return 0, false
	}

	if access := requestAgent(r.Context()); access != nil && payload.ID != access.PostID {
		postJSON(w, 403, map[string]string{"error": "agent credential does not allow this task"})
		return 0, false
	}

	return payload.ID, true
}

func escrowError(w http.ResponseWriter, err error) {
	log.Printf("post escrow: %v", err)

	message := "Solana service unavailable; refresh to check transaction status before retrying"
	if s, ok := status.FromError(err); ok {
		message = s.Message()
	}

	postJSON(w, http.StatusServiceUnavailable, map[string]string{"error": message})
}

func (c *Controller) AcceptPost(w http.ResponseWriter, r *http.Request) {
	userID, ok := postUserID(w, r)
	if !ok {
		return
	}

	id, ok := postIDPayload(w, r)
	if !ok {
		return
	}

	post, err := c.queries.AcceptPost(
		r.Context(),
		db.AcceptPostParams{WorkerID: pgtype.Int8{Int64: userID, Valid: true}, PostID: id},
	)
	if errors.Is(err, pgx.ErrNoRows) {
		c.acceptPostUnavailable(w, r, id, userID)
		return
	}

	if err != nil {
		log.Printf("accept post: %v", err)
		postJSON(w, 500, map[string]string{"error": "failed to accept post"})

		return
	}

	c.writePost(
		w, r, 200, post,
		map[string]any{
			"message": "Accepted. Wait for the poster to fund escrow before starting work.",
		},
	)
}

func (c *Controller) acceptPostUnavailable(w http.ResponseWriter, r *http.Request, id, userID int64) {
	post, err := c.queries.GetPost(r.Context(), id)
	if errors.Is(err, pgx.ErrNoRows) {
		postJSON(w, 404, map[string]string{"error": "post not found"})
		return
	}

	if err != nil {
		log.Printf("check post availability: %v", err)
		postJSON(w, 500, map[string]string{"error": "failed to check post availability"})

		return
	}

	message := "post changed; refresh and try again"

	switch {
	case post.UserID == userID:
		message = "you cannot accept your own post"
	case post.AcceptedBy.Valid:
		message = "this post has already been accepted; refresh to see its status"
	case post.Status != db.PostStatusOpen:
		message = "this post is no longer open"
	case !post.EndTime.Time.After(time.Now()):
		message = "the deadline to accept this post has passed"
	default:
		for _, participant := range []struct {
			id      int64
			message string
		}{
			{userID, "connect your wallet in the profile menu before accepting"},
			{post.UserID, "the poster must connect their wallet in the profile menu before you can accept"},
		} {
			_, err := c.queries.GetWallet(r.Context(), participant.id)
			if errors.Is(err, pgx.ErrNoRows) {
				message = participant.message
				break
			}

			if err != nil {
				log.Printf("check acceptance wallet: %v", err)
				postJSON(w, 500, map[string]string{"error": "failed to check wallet connection"})

				return
			}
		}
	}

	postJSON(w, 409, map[string]string{"error": message})
}

func (c *Controller) PostInfo(w http.ResponseWriter, r *http.Request) {
	userID, ok := postUserID(w, r)
	if !ok {
		return
	}

	id, ok := postIDPayload(w, r)
	if !ok {
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()

	tx, err := c.pool.Begin(ctx)
	if err != nil {
		escrowError(w, err)
		return
	}
	defer tx.Rollback(ctx)

	q := db.New(tx)

	post, err := q.LockPost(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		postJSON(w, 404, map[string]string{"error": "post not found"})
		return
	}

	if err != nil {
		escrowError(w, err)
		return
	}

	if post.UserID != userID && post.Status != db.PostStatusOpen &&
		!workspaceAccess(w, r, q, post, nil, userID) {
		return
	}

	escrow, err := q.GetPostEscrow(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		c.writePost(w, r, 200, post, map[string]any{"escrow": fundingView{State: "unfunded"}})
		return
	}

	if err != nil {
		escrowError(w, err)
		return
	}

	post, escrow, err = c.syncEscrow(ctx, q, post, escrow, false)
	if err != nil {
		escrowError(w, err)
		return
	}

	if err = tx.Commit(ctx); err != nil {
		escrowError(w, err)
		return
	}

	c.writePost(w, r, 200, post, map[string]any{"escrow": escrowView(escrow, false)})
}

func (c *Controller) PreparePostFunding(w http.ResponseWriter, r *http.Request) {
	userID, ok := postUserID(w, r)
	if !ok {
		return
	}

	id, ok := postIDPayload(w, r)
	if !ok {
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()

	tx, err := c.pool.Begin(ctx)
	if err != nil {
		escrowError(w, err)
		return
	}
	defer tx.Rollback(ctx)

	q := db.New(tx)

	post, err := q.LockPost(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		postJSON(w, 404, map[string]string{"error": "post not found"})
		return
	}

	if err != nil {
		escrowError(w, err)
		return
	}

	if post.UserID != userID {
		postJSON(w, 403, map[string]string{"error": "only the poster can fund escrow"})
		return
	}

	if !post.AcceptedBy.Valid {
		postJSON(w, 409, map[string]string{"error": "a worker must accept this post first"})
		return
	}

	escrow, err := q.GetPostEscrow(ctx, id)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		escrowError(w, err)
		return
	}

	if err == nil {
		post, escrow, err = c.syncEscrow(ctx, q, post, escrow, false)
		if err != nil {
			escrowError(w, err)
			return
		}

		if escrow.State != "expired" && escrow.State != "failed" {
			if err = tx.Commit(ctx); err != nil {
				escrowError(w, err)
				return
			}

			c.writePost(w, r, 200, post, map[string]any{"escrow": escrowView(escrow, true)})

			return
		}
	}

	if post.Status != db.PostStatusNegotiating {
		postJSON(w, 409, map[string]string{"error": "post cannot be funded in its current state"})
		return
	}

	version := int32(2)
	if post.DeliverBy.Valid {
		version = 3
	}

	plan, err := pb.NewSolanaServiceClient(c.rpc).PrepareEscrow(ctx, agreement(post, version))
	if err != nil {
		escrowError(w, err)
		return
	}

	escrow, err = q.SavePostEscrow(
		ctx,
		db.SavePostEscrowParams{
			AgreementVersion:     version,
			PostID:               id,
			Address:              plan.Address,
			ProgramID:            plan.ProgramId,
			Reviewer:             plan.Reviewer,
			Network:              plan.Network,
			Transaction:          plan.Transaction,
			LastValidBlockHeight: int64(plan.LastValidBlockHeight),
			FeeLamports:          int64(plan.FeeLamports),
			StorageLamports:      int64(plan.StorageLamports),
		},
	)
	if err != nil {
		escrowError(w, err)
		return
	}

	if err = tx.Commit(ctx); err != nil {
		escrowError(w, err)
		return
	}

	c.writePost(w, r, 200, post, map[string]any{"escrow": escrowView(escrow, true)})
}

func (c *Controller) SubmitPostFunding(w http.ResponseWriter, r *http.Request) {
	userID, ok := postUserID(w, r)
	if !ok {
		return
	}

	var payload struct {
		ID          int64  `json:"id"`
		Transaction string `json:"transaction"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&payload) != nil || payload.ID <= 0 {
		postJSON(w, 400, map[string]string{"error": "invalid funding payload"})
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()

	tx, err := c.pool.Begin(ctx)
	if err != nil {
		escrowError(w, err)
		return
	}
	defer tx.Rollback(ctx)

	q := db.New(tx)

	post, err := q.LockPost(ctx, payload.ID)
	if errors.Is(err, pgx.ErrNoRows) {
		postJSON(w, 404, map[string]string{"error": "post not found"})
		return
	}

	if err != nil {
		escrowError(w, err)
		return
	}

	if post.UserID != userID {
		postJSON(w, 403, map[string]string{"error": "only the poster can fund escrow"})
		return
	}

	escrow, err := q.GetPostEscrow(ctx, payload.ID)
	if err != nil {
		postJSON(w, 409, map[string]string{"error": "prepare funding first"})
		return
	}

	signature, err := utils.TransactionSignature(escrow.Transaction, payload.Transaction, post.PosterWallet)
	if err != nil {
		postJSON(w, 400, map[string]string{"error": err.Error()})
		return
	}

	if escrow.State == "confirmed" && escrow.Signature == signature {
		c.writePost(w, r, 200, post, map[string]any{"escrow": escrowView(escrow, false)})
		return
	}

	if post.Status != db.PostStatusNegotiating {
		postJSON(w, 409, map[string]string{"error": "this task no longer accepts funding"})
		return
	}

	if escrow.State == "prepared" ||
		(escrow.State == "pending" && escrow.Signature == signature && escrow.SignedTransaction == "") {
		escrow, err = q.MarkEscrowSubmitted(
			ctx,
			db.MarkEscrowSubmittedParams{
				PostID:            post.ID,
				Signature:         signature,
				Transaction:       escrow.Transaction,
				SignedTransaction: payload.Transaction,
			},
		)
		if err != nil {
			escrowError(w, err)
			return
		}
	} else if escrow.State != "pending" || escrow.Signature != signature {
		postJSON(w, 409, map[string]string{"error": "funding already submitted; refresh this post"})
		return
	}
	// Persist the signed transaction before RPC so the monitor can retry after a restart.
	if err = tx.Commit(ctx); err != nil {
		escrowError(w, err)
		return
	}

	_, err = pb.NewSolanaServiceClient(c.rpc).
		SubmitEscrow(ctx, &pb.SubmitEscrowRequest{Transaction: escrow.Transaction, SignedTransaction: payload.Transaction})
	if err != nil {
		escrowError(w, err)
		return
	}

	c.writePost(
		w, r, http.StatusAccepted, post,
		map[string]any{
			"escrow":  escrowView(escrow, false),
			"message": "Funding submitted. Refresh to verify confirmation.",
		},
	)
}

func (c *Controller) syncEscrow(
	ctx context.Context,
	q *db.Queries,
	post db.Post,
	escrow db.PostEscrow,
	retry bool,
) (db.Post, db.PostEscrow, error) {
	client := pb.NewSolanaServiceClient(c.rpc)

	result, err := client.CheckEscrow(ctx, checkRequest(post, escrow))
	if err != nil {
		return post, escrow, err
	}

	switch result.State {
	case "prepared", "pending", "confirmed", "failed", "expired", "released", "refunded":
	default:
		return post, escrow, errors.New("unknown on-chain funding state")
	}

	if escrow.State == "confirmed" &&
		(result.State == "prepared" || result.State == "pending" || result.State == "expired" || result.State == "failed") {
		return post, escrow, errors.New("confirmed escrow missing from this network")
	}

	if (escrow.State == "released" || escrow.State == "refunded") && result.State != escrow.State {
		return post, escrow, errors.New("settled escrow changed on this network")
	}

	if result.State != escrow.State {
		err = q.UpdateEscrowState(ctx, db.UpdateEscrowStateParams{PostID: post.ID, State: result.State})
		if err != nil {
			return post, escrow, err
		}

		escrow.State = result.State
	}

	if retry && result.State == "pending" && escrow.SignedTransaction != "" {
		_, _ = client.SubmitEscrow(ctx, &pb.SubmitEscrowRequest{
			Transaction: escrow.Transaction, SignedTransaction: escrow.SignedTransaction,
		})
	}

	if result.State == "confirmed" && post.Status == db.PostStatusNegotiating {
		post, err = q.MarkPostFunded(ctx, post.ID)
	}

	if result.State == "released" && post.Status != db.PostStatusCompleted {
		post, err = q.MarkPostSettled(
			ctx,
			db.MarkPostSettledParams{ID: post.ID, Status: db.PostStatusCompleted},
		)
	}

	if result.State == "refunded" && post.Status != db.PostStatusCancelled {
		post, err = q.MarkPostSettled(
			ctx,
			db.MarkPostSettledParams{ID: post.ID, Status: db.PostStatusCancelled},
		)
	}

	if err == nil && (result.State == "released" || result.State == "refunded") {
		reviewState := "approved"
		if result.State == "refunded" {
			reviewState = "refunded"
		}

		err = q.CloseWorkspaceReview(
			ctx,
			db.CloseWorkspaceReviewParams{PostID: post.ID, ReviewState: reviewState},
		)
	}

	return post, escrow, err
}
