package controller

import (
	"errors"
	"fmt"
	"testing"

	"github.com/jackc/pgx/v5"
	db "github.com/rajandhamala/Maruvo/db/sqlc"
)

func TestFundingRetryChangesReviewerOnlyAfterFailureOrExpiry(t *testing.T) {
	f := newAgentAccessFixture(t)
	plan := db.SavePostEscrowParams{
		PostID: f.postID, Address: fmt.Sprintf("requester-review-retry-%d", f.postID), ProgramID: "fixture",
		Reviewer: "legacy-reviewer", Network: "localnet", Transaction: "old-plan",
		LastValidBlockHeight: 1, AgreementVersion: 2,
	}
	q := f.controller.queries
	if _, err := q.SavePostEscrow(f.ctx, plan); err != nil {
		t.Fatal(err)
	}
	for _, state := range []string{"prepared", "pending", "confirmed", "released", "refunded", "failed", "expired"} {
		if _, err := f.pool.Exec(f.ctx, "UPDATE post_escrows SET state=$2, reviewer='legacy-reviewer' WHERE post_id=$1", f.postID, state); err != nil {
			t.Fatal(err)
		}
		replacement := plan
		replacement.Reviewer, replacement.Transaction = "requester-wallet", "new-plan"
		saved, err := q.SavePostEscrow(f.ctx, replacement)
		if state == "failed" || state == "expired" {
			if err != nil || saved.Reviewer != replacement.Reviewer || saved.State != "prepared" {
				t.Fatalf("%s funding could not move review to requester: %v %+v", state, err, saved)
			}
		} else if !errors.Is(err, pgx.ErrNoRows) {
			t.Fatalf("%s reviewer was replaced: %v", state, err)
		}
	}
}
