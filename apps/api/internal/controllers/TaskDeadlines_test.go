package controller

import (
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	db "github.com/rajandhamala/Maruvo/db/sqlc"
)

func TestTaskTimingValidation(t *testing.T) {
	acceptBy := time.Date(2026, 10, 4, 10, 0, 0, 0, time.UTC)
	for _, test := range []struct {
		fund, review int64
		deliver      time.Time
		valid        bool
	}{
		{0, 0, time.Time{}, true},
		{60, 60, acceptBy.Add(2 * time.Minute), true},
		{30 * 86400, 30 * 86400, acceptBy.Add(31 * 24 * time.Hour), true},
		{60, 60, acceptBy.Add(time.Minute), false},
		{60, 60, time.Time{}, false},
		{0, 60, acceptBy.Add(time.Hour), false},
		{-1, 60, acceptBy.Add(time.Hour), false},
		{60, 30*86400 + 1, acceptBy.Add(time.Hour), false},
	} {
		if err := validateTaskTiming(
			acceptBy,
			test.deliver,
			test.fund,
			test.review,
		); (err == nil) != test.valid {
			t.Fatalf("fund=%d review=%d deliver=%v: %v", test.fund, test.review, test.deliver, err)
		}
	}
}

func TestDeadlineStagesAndBoundary(t *testing.T) {
	now := time.Date(2026, 10, 4, 10, 0, 0, 0, time.UTC)

	due := pgtype.Timestamptz{Time: now, Valid: true}
	for _, test := range []struct {
		status        db.PostStatus
		review, stage string
	}{
		{db.PostStatusOpen, "", "accept"},
		{db.PostStatusNegotiating, "working", "fund"},
		{db.PostStatusInProgress, "working", "deliver"},
		{db.PostStatusInProgress, "changes_requested", "deliver"},
		{db.PostStatusInProgress, "submitted", "review"},
		{db.PostStatusCompleted, "approved", ""},
		{db.PostStatusCancelled, "refunded", ""},
	} {
		snapshot := db.TaskDeadlineSnapshotsRow{Status: test.status, ReviewState: test.review,
			EndTime: due, FundBy: due, DeliverBy: due, ReviewBy: due}

		result := deadlineStatus(snapshot, now)
		if result.Stage != test.stage || result.Overdue != (test.stage != "") {
			t.Fatalf("%s/%s: %+v", test.status, test.review, result)
		}

		if deadlineStatus(snapshot, now.Add(-time.Nanosecond)).Overdue {
			t.Fatal("a future deadline must not be overdue")
		}
	}

	legacy := deadlineStatus(db.TaskDeadlineSnapshotsRow{Status: db.PostStatusNegotiating}, now)
	if legacy.DueAt != nil || legacy.Stage != "" || legacy.Overdue {
		t.Fatal("existing tasks must not acquire implicit timing terms")
	}

	late := deadlineStatus(db.TaskDeadlineSnapshotsRow{Status: db.PostStatusCompleted,
		DeliverBy: due, SubmittedAt: pgtype.Timestamptz{Time: now.Add(time.Second), Valid: true}}, now)
	if !late.DeliveryLate || late.Overdue {
		t.Fatal("completed tasks retain late-delivery history without active overdue state")
	}
}
