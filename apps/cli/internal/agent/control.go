package agent

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/rajandhamala/Maruvo/cli/internal/api"
)

var errAgentPaused = errors.New("agent access is paused for this task; switch to agent mode to resume")

func agentPaused(err error) bool {
	var failure *api.Error

	return errors.Is(err, errAgentPaused) || (errors.As(err, &failure) && failure.StatusCode == 409 &&
		strings.HasPrefix(failure.Message, "agent access is paused for this task"))
}

func controlledTaskContext(ctx context.Context, client *api.Client, token string, postID int64) (
	context.Context, func(), error,
) {
	check := func(ctx context.Context) error {
		control, err := client.AgentControl(ctx, token, postID)
		if err != nil {
			return err
		}

		if control.Mode != "agent" {
			return errAgentPaused
		}

		return nil
	}
	if err := check(ctx); err != nil {
		return nil, nil, err
	}

	runCtx, cancel := context.WithCancelCause(ctx)
	done := make(chan struct{})

	go func() {
		defer close(done)

		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()

		for {
			select {
			case <-runCtx.Done():
				return
			case <-ticker.C:
				probe, stop := context.WithTimeout(runCtx, 5*time.Second)
				err := check(probe)

				stop()

				if err != nil {
					cancel(err)
					return
				}
			}
		}
	}()

	return runCtx, func() { cancel(nil); <-done }, nil
}
