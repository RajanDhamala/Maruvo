package agent

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"time"

	"github.com/rajandhamala/Maruvo/cli/internal/api"
)

type waitResult struct {
	Status string              `json:"status"`
	PostID int64               `json:"post_id"`
	Cursor string              `json:"cursor"`
	Event  *api.WorkspaceEvent `json:"event,omitempty"`
}

func waitForTask(
	ctx context.Context, client *api.Client, token string, id, after int64,
	cursor string, kinds []string, timeout time.Duration,
) (waitResult, error) {
	waitCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	result := waitResult{Status: "timeout", PostID: id, Cursor: cursor}

	workspace, err := client.Workspace(waitCtx, token, id)
	if err != nil {
		return result, err
	}

	if cursor == "" && after == 0 {
		cursor = workspace.Cursor
		if cursor == "" {
			cursor = "0-0"
		}

		result.Cursor = cursor
	}

	if workspace.Context.Terminal || workspace.Post.Status == "completed" ||
		workspace.Post.Status == "cancelled" {
		result.Status = "closed"
		result.Cursor = workspace.Cursor

		return result, nil
	}

	done := errors.New("wait complete")

	err = watch(waitCtx, client, token, id, after, cursor, func(frame api.StreamFrame) error {
		if frame.Event == "connected" {
			var connected struct {
				Cursor string `json:"cursor"`
			}
			if err := json.Unmarshal(frame.Data, &connected); err != nil {
				return err
			}

			if result.Cursor != "" && connected.Cursor != result.Cursor {
				result.Status, result.Cursor = "resync", connected.Cursor
				return done
			}

			result.Cursor = connected.Cursor
		}

		if frame.Event != "workspace.event" {
			return nil
		}

		var event api.WorkspaceEvent
		if err := json.Unmarshal(frame.Data, &event); err != nil {
			return err
		}

		result.Cursor = event.StreamID

		var state struct {
			Status      string `json:"status"`
			State       string `json:"state"`
			ReviewState string `json:"review_state"`
		}
		if event.Kind == "task.status" || event.Kind == "escrow.updated" || event.Kind == "review.completed" {
			if err := json.Unmarshal(event.Data, &state); err != nil {
				return err
			}

			if state.Status == "completed" || state.Status == "cancelled" ||
				state.State == "released" || state.State == "refunded" ||
				state.ReviewState == "approved" || state.ReviewState == "refunded" {
				result.Status, result.Event = "closed", &event
				return done
			}
		}

		if len(kinds) == 0 || slices.Contains(kinds, event.Kind) {
			result.Status, result.Event = "event", &event
			return done
		}

		return nil
	})
	if errors.Is(err, done) {
		return result, nil
	}

	if ctx.Err() == nil && errors.Is(waitCtx.Err(), context.DeadlineExceeded) {
		return result, nil
	}

	return result, err
}
