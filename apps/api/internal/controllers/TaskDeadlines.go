package controller

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	db "github.com/rajandhamala/Maruvo/db/sqlc"
)

type taskDeadline struct {
	Stage          string     `json:"stage"`
	DueAt          *time.Time `json:"due_at"`
	Overdue        bool       `json:"overdue"`
	RecoveryAction string     `json:"recovery_action"`
	DeliveryLate   bool       `json:"delivery_late"`
}

func validateTaskTiming(acceptBy, deliverBy time.Time, funding, review int64) error {
	if funding == 0 && review == 0 && deliverBy.IsZero() {
		return nil
	}

	if funding < 60 || funding > 30*86400 || review < 60 || review > 30*86400 {
		return errors.New("funding and review windows must each be between one minute and 30 days")
	}

	if !deliverBy.After(acceptBy.Add(time.Duration(funding) * time.Second)) {
		return errors.New("choose a delivery deadline after the acceptance cutoff plus the funding window")
	}

	return nil
}

func deadlineStatus(snapshot db.TaskDeadlineSnapshotsRow, now time.Time) taskDeadline {
	var due pgtype.Timestamptz

	result := taskDeadline{}
	if snapshot.DeliverBy.Valid && snapshot.SubmittedAt.Valid {
		result.DeliveryLate = snapshot.SubmittedAt.Time.After(snapshot.DeliverBy.Time)
	}

	switch snapshot.Status {
	case db.PostStatusOpen:
		result.Stage, due = "accept", snapshot.EndTime
	case db.PostStatusNegotiating:
		result.Stage, due = "fund", snapshot.FundBy
		result.RecoveryAction = "cancel_or_reopen"
	case db.PostStatusInProgress:
		if snapshot.ReviewState == "submitted" {
			result.Stage, due = "review", snapshot.ReviewBy
		} else if snapshot.ReviewState == "working" || snapshot.ReviewState == "changes_requested" {
			result.Stage, due = "deliver", snapshot.DeliverBy
		}

		result.RecoveryAction = "reviewer_decision"
	}

	if !due.Valid {
		result.Stage, result.RecoveryAction = "", ""
		return result
	}

	result.DueAt = &due.Time

	result.Overdue = !now.Before(due.Time)
	if !result.Overdue {
		result.RecoveryAction = ""
	}

	return result
}

func (c *Controller) RunDeadlineMonitor(ctx context.Context) {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	cursor := int64(0)
	for {
		ids, err := c.queries.OverdueTaskCandidates(ctx, cursor)
		if err != nil && ctx.Err() == nil {
			log.Printf("deadline monitor: %v", err)
		}

		for _, id := range ids {
			if ctx.Err() != nil {
				return
			}

			if err := c.notifyTaskDeadline(ctx, id); err != nil && ctx.Err() == nil {
				log.Printf("task %d deadline: %v", id, err)
			}

			cursor = id
		}

		if len(ids) < 100 {
			cursor = 0
		}

		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (c *Controller) notifyTaskDeadline(parent context.Context, id int64) error {
	ctx, cancel := context.WithTimeout(parent, 5*time.Second)
	defer cancel()

	tx, err := c.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	q := db.New(tx)
	if _, err := q.LockPost(ctx, id); err != nil {
		return err
	}

	snapshots, err := q.TaskDeadlineSnapshots(ctx, []int64{id})
	if err != nil {
		return err
	}

	if len(snapshots) != 1 {
		return pgx.ErrNoRows
	}

	deadline := deadlineStatus(snapshots[0], time.Now())
	if !deadline.Overdue || deadline.Stage == "accept" {
		return nil
	}

	workspace, err := q.GetWorkspace(ctx, id)
	if err != nil {
		return err
	}

	version := int64(0)
	if deadline.Stage == "review" {
		version = workspace.SubmissionVersion
	}

	_, err = q.RecordDeadlineNotice(ctx, db.RecordDeadlineNoticeParams{
		PostID: id, Stage: deadline.Stage, SubmissionVersion: version,
		DueAt: pgtype.Timestamptz{Time: *deadline.DueAt, Valid: true},
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}

	if err != nil {
		return err
	}

	data, _ := json.Marshal(map[string]any{"stage": deadline.Stage, "due_at": deadline.DueAt,
		"submission_version": version, "recovery_action": deadline.RecoveryAction})
	if _, err = q.AppendWorkspaceEvent(ctx, db.AppendWorkspaceEventParams{
		PostID: id, Kind: "task.overdue", Data: data,
	}); err != nil {
		return err
	}

	return tx.Commit(ctx)
}
