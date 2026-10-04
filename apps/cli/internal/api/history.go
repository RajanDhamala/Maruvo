package api

import (
	"context"
	"fmt"
	"net/http"
)

type TaskContext struct {
	Role                 string            `json:"role"`
	Terminal             bool              `json:"terminal"`
	SubmissionVersion    int64             `json:"submission_version"`
	AllowedActions       []string          `json:"allowed_actions"`
	BlockedActions       map[string]string `json:"blocked_actions"`
	UploadPurposes       []string          `json:"upload_purposes"`
	WaitingFor           []string          `json:"waiting_for"`
	InputFileIDs         []string          `json:"input_file_ids"`
	MissingInputs        []string          `json:"missing_inputs"`
	MissingOutputs       []string          `json:"missing_outputs"`
	HumanPaymentRequired bool              `json:"human_payment_required"`
}

type TaskHistory struct {
	PostID     int64            `json:"post_id"`
	Events     []WorkspaceEvent `json:"events"`
	HasMore    bool             `json:"has_more"`
	NextBefore int64            `json:"next_before"`
}

func (c *Client) History(
	ctx context.Context,
	token string,
	id, before int64,
	limit int,
) (TaskHistory, error) {
	var result TaskHistory

	path := fmt.Sprintf("%s/history?before=%d&limit=%d", workspacePath(id), before, limit)
	err := c.request(ctx, http.MethodGet, path, token, nil, &result)

	return result, err
}
