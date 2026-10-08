package api

import (
	"context"
	"net/http"
	"time"
)

type RemoteStatus struct {
	Status            string     `json:"status"`
	AgentName         string     `json:"agent_name"`
	WorkerOnline      bool       `json:"worker_online"`
	WorkerSeen        *time.Time `json:"worker_seen,omitempty"`
	Detail            string     `json:"detail"`
	SubmissionVersion int64      `json:"submission_version"`
	LastEventID       int64      `json:"last_event_id"`
}

func (c *Client) RemoteInbox(ctx context.Context, token string) ([]Post, error) {
	var result struct {
		Posts []Post `json:"posts"`
	}

	err := c.request(ctx, http.MethodGet, "/agent/inbox", token, nil, &result)

	return result.Posts, err
}

func (c *Client) ReportActivity(
	ctx context.Context,
	token string,
	id int64,
	runID, state, detail string,
) error {
	return c.requestJSON(ctx, http.MethodPost, workspacePath(id)+"/activity", token,
		map[string]string{"run_id": runID, "state": state, "detail": detail}, nil)
}
