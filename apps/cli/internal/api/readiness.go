package api

import (
	"context"
	"net/http"
)

type AgentReadiness struct {
	You        bool   `json:"you_ready"`
	Peer       bool   `json:"peer_ready"`
	Pair       string `json:"pair"`
	PeerStatus string `json:"peer_status"`
	PeerDetail string `json:"peer_detail"`
}

func (c *Client) Readiness(ctx context.Context, token string, id int64, ready *bool, nonce string) (AgentReadiness, error) {
	var result AgentReadiness
	var err error
	if ready == nil {
		err = c.request(ctx, http.MethodGet, workspacePath(id)+"/agent-ready", token, nil, &result)
	} else {
		err = c.requestJSON(ctx, http.MethodPost, workspacePath(id)+"/agent-ready", token, map[string]any{"ready": *ready, "nonce": nonce}, &result)
	}
	return result, err
}

func (c *Client) InviteReadiness(ctx context.Context, token string, id int64, ready bool, nonce string, invite bool, target string, decline bool, activity ...string) (AgentReadiness, error) {
	var result AgentReadiness
	body := map[string]any{"ready": ready, "nonce": nonce, "invite": invite, "target": target, "decline": decline}
	if len(activity) == 2 {
		body["status"], body["detail"] = activity[0], activity[1]
	}
	err := c.requestJSON(ctx, http.MethodPost, workspacePath(id)+"/agent-ready", token, body, &result)
	return result, err
}
