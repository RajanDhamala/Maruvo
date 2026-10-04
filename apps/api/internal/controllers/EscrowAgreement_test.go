package controller

import (
	"encoding/hex"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	db "github.com/rajandhamala/Maruvo/db/sqlc"
)

func TestAgreementCompatibility(t *testing.T) {
	deadline, err := time.Parse(time.RFC3339Nano, "2026-10-03T04:05:06.123456Z")
	if err != nil {
		t.Fatal(err)
	}

	post := db.Post{
		ID: 11, UserID: 1, AcceptedBy: pgtype.Int8{Int64: 2, Valid: true},
		PosterWallet: "poster", WorkerWallet: "worker", CostLamports: 1000000,
		EndTime: pgtype.Timestamptz{Time: deadline, Valid: true}, Level: db.PostLevelEasy,
		Title: `Fix "parser"`, Description: "Read\nUTF-8: café <data>",
		AcceptanceCriteria: "All tests pass", InputFiles: []string{"source.txt", "input.csv"},
		ExpectedOutputs: []string{"patch.diff", "tests.log"},
	}
	post.FundingWindowSeconds, post.ReviewWindowSeconds = 7200, 86400
	post.FundBy = pgtype.Timestamptz{Time: deadline.Add(2 * time.Hour), Valid: true}
	post.DeliverBy = pgtype.Timestamptz{Time: deadline.Add(48 * time.Hour), Valid: true}
	// These vectors also run against the independent CLI encoder.
	for version, expected := range map[int32]string{
		1: "59d9f2b50b4a026cc306a42a5ca506dd914302208c0e2e0f76578344a91436f5",
		2: "ad76188263bc8723d26988c6e5dac8ff643c1a750850fa1d91d8a93d78b7c56e",
		3: "4e08a522ed4285cbe7dce83d977e3fba3ee3a7a99c012ba88c8d6e0597a3237a",
	} {
		if got := hex.EncodeToString(agreement(post, version).AgreementHash); got != expected {
			t.Fatalf("v%d: got %s, want %s", version, got, expected)
		}
	}

	post.InputFiles, post.ExpectedOutputs = nil, nil
	nilHash := hex.EncodeToString(agreement(post, 2).AgreementHash)

	post.InputFiles, post.ExpectedOutputs = []string{}, []string{}
	if hex.EncodeToString(agreement(post, 2).AgreementHash) != nilHash {
		t.Fatal("empty file declarations must have one encoding")
	}
}

func TestSettlementEligibility(t *testing.T) {
	for _, test := range []struct {
		state              string
		version, requested int64
		action             string
		allowed            bool
	}{
		{"working", 0, 0, "refund", true},
		{"changes_requested", 2, 2, "refund", true},
		{"submitted", 2, 2, "refund", true},
		{"submitted", 2, 2, "release", true},
		{"working", 0, 0, "release", false},
		{"changes_requested", 2, 2, "release", false},
		{"working", 0, 1, "refund", false},
		{"submitted", 2, 1, "refund", false},
		{"refunded", 0, 0, "refund", false},
		{"approved", 2, 2, "refund", false},
		{"submitted", 0, 0, "release", false},
	} {
		workspace := db.PostWorkspace{ReviewState: test.state, SubmissionVersion: test.version}
		if canSettle(workspace, test.action, test.requested) != test.allowed {
			t.Errorf(
				"%s v%d, %s v%d: want allowed=%v",
				test.state,
				test.version,
				test.action,
				test.requested,
				test.allowed,
			)
		}
	}
}
