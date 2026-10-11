package controller

import (
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	db "github.com/rajandhamala/Maruvo/db/sqlc"
)

func TestRemoteStatusSeparatesPresenceFromExecution(t *testing.T) {
	now := time.Now()
	stamp := func(offset time.Duration) pgtype.Timestamptz {
		return pgtype.Timestamptz{Time: now.Add(offset), Valid: true}
	}

	worker := pgtype.Int8{Int64: 2, Valid: true}
	for _, test := range []struct {
		name     string
		post     db.Post
		snapshot db.RemoteSnapshotsRow
		status   string
		online   bool
	}{
		{"offline queue", db.Post{Status: db.PostStatusOpen, TargetWorker: worker}, db.RemoteSnapshotsRow{}, "queued", false},
		{"queued online", db.Post{Status: db.PostStatusOpen}, db.RemoteSnapshotsRow{OfferUntil: stamp(time.Minute)}, "queued", true},
		{"funding gate", db.Post{AcceptedBy: worker}, db.RemoteSnapshotsRow{ActivityState: "working", ActiveUntil: stamp(time.Minute)}, "waiting_for_funding", true},
		{"online idle", db.Post{AcceptedBy: worker}, db.RemoteSnapshotsRow{EscrowState: "confirmed", OfferUntil: stamp(time.Minute)}, "waiting_for_worker", true},
		{"execution", db.Post{AcceptedBy: worker}, db.RemoteSnapshotsRow{EscrowState: "confirmed", ActivityState: "working", ActiveUntil: stamp(time.Minute), WorkerSeen: stamp(0)}, "working", true},
		{"lost execution while seller online", db.Post{AcceptedBy: worker}, db.RemoteSnapshotsRow{EscrowState: "confirmed", ActivityState: "working", ActiveUntil: stamp(-time.Second), WorkerSeen: stamp(-time.Minute), OfferUntil: stamp(time.Minute)}, "interrupted", true},
		{"offline question persists", db.Post{AcceptedBy: worker}, db.RemoteSnapshotsRow{EscrowState: "confirmed", ActivityState: "waiting_for_answer", WorkerSeen: stamp(-time.Hour)}, "waiting_for_answer", false},
		{"offline delivery persists", db.Post{AcceptedBy: worker}, db.RemoteSnapshotsRow{EscrowState: "confirmed", ReviewState: "submitted"}, "waiting_for_review", false},
		{"failed", db.Post{AcceptedBy: worker}, db.RemoteSnapshotsRow{EscrowState: "confirmed", ActivityState: "failed"}, "failed", false},
		{"revision after delivery", db.Post{AcceptedBy: worker}, db.RemoteSnapshotsRow{EscrowState: "confirmed", ReviewState: "changes_requested", ActivityState: "waiting_for_review", WorkerSeen: stamp(0), ActiveUntil: stamp(-time.Second)}, "waiting_for_worker", false},
		{"completed wins", db.Post{Status: db.PostStatusCompleted}, db.RemoteSnapshotsRow{ActivityState: "working"}, "completed", false},
	} {
		t.Run(test.name, func(t *testing.T) {
			got := remoteStatus(test.post, test.snapshot, now)
			if got.Status != test.status || got.WorkerOnline != test.online {
				t.Fatalf("got %+v", got)
			}
		})
	}
}
