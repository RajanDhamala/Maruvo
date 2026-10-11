package api

import (
	"context"
	"net/http"
	"time"
)

type OfferTerms struct {
	Name              string   `json:"name"`
	Description       string   `json:"description"`
	Capabilities      []string `json:"capabilities"`
	MinLamports       int64    `json:"min_lamports"`
	JobTimeoutSeconds int64    `json:"job_timeout_seconds"`
}

type AgentOffer struct {
	OfferTerms
	UserID         int64      `json:"user_id"`
	Username       string     `json:"username,omitempty"`
	GitHubLogin    string     `json:"github_login,omitempty"`
	Available      bool       `json:"available"`
	Online         bool       `json:"online"`
	AvailableUntil *time.Time `json:"available_until"`
	UpdatedAt      time.Time  `json:"updated_at"`
}

func (c *Client) AgentOffers(ctx context.Context, token string) ([]AgentOffer, error) {
	var result []AgentOffer

	err := c.request(ctx, http.MethodGet, "/agent-offers", token, nil, &result)

	return result, err
}

func (c *Client) OwnAgentOffer(ctx context.Context, token string) (AgentOffer, error) {
	var result AgentOffer

	err := c.request(ctx, http.MethodGet, "/agent-offers/mine", token, nil, &result)

	return result, err
}

func (c *Client) SaveAgentOffer(ctx context.Context, token string, terms OfferTerms) (AgentOffer, error) {
	var result AgentOffer

	err := c.requestJSON(ctx, http.MethodPut, "/agent-offers/mine", token, terms, &result)

	return result, err
}

func (c *Client) LeaseAgentOffer(
	ctx context.Context,
	token, lease string,
	accepting bool,
) (AgentOffer, error) {
	var result AgentOffer

	err := c.requestJSON(ctx, http.MethodPost, "/agent-offers/mine/lease", token,
		map[string]any{"lease": lease, "accepting": accepting}, &result)

	return result, err
}

func (c *Client) ReleaseAgentOffer(ctx context.Context, token, lease string) error {
	return c.requestJSON(ctx, http.MethodDelete, "/agent-offers/mine/lease", token,
		map[string]string{"lease": lease}, nil)
}

func (c *Client) ClaimAgentTask(ctx context.Context, token, lease string, id int64) (Post, error) {
	var result struct {
		Post Post `json:"post"`
	}

	err := c.requestJSON(ctx, http.MethodPost, "/posts/accept", token,
		map[string]any{"id": id, "offer_lease": lease}, &result)

	return result.Post, err
}
