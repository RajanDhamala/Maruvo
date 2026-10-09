package api

import (
	"context"
	"net/http"
)

type WorkspacePresence struct {
	RequesterOnline bool `json:"requester_online"`
	WorkerOnline    bool `json:"worker_online"`
}

func (c *Client) WorkspacePresence(ctx context.Context, token string, id int64) (WorkspacePresence, error) {
	var result WorkspacePresence
	err := c.request(ctx, http.MethodGet, workspacePath(id)+"/presence", token, nil, &result)
	return result, err
}
