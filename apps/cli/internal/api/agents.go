package api

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

type AgentGrant struct {
	ID          string     `json:"id"`
	OwnerID     int64      `json:"owner_id"`
	PostID      int64      `json:"post_id"`
	Name        string     `json:"name"`
	Permissions []string   `json:"permissions"`
	ExpiresAt   time.Time  `json:"expires_at"`
	CreatedAt   *time.Time `json:"created_at,omitempty"`
	RevokedAt   *time.Time `json:"revoked_at,omitempty"`
}

type AgentCredential struct {
	APIURL string     `json:"api_url"`
	Token  string     `json:"token"`
	Grant  AgentGrant `json:"grant"`
}

type AgentControl struct {
	PostID        int64      `json:"post_id"`
	OwnerID       int64      `json:"owner_id"`
	Mode          string     `json:"mode"`
	UpdatedAt     *time.Time `json:"updated_at,omitempty"`
	RevokedGrants int64      `json:"revoked_grants"`
}

func (c *Client) AgentControl(ctx context.Context, token string, postID int64) (AgentControl, error) {
	var control AgentControl

	err := c.request(ctx, http.MethodGet, workspacePath(postID)+"/agent-control", token, nil, &control)

	return control, err
}

func (c *Client) SetAgentControl(
	ctx context.Context,
	token string,
	postID int64,
	mode string,
) (AgentControl, error) {
	var control AgentControl

	err := c.requestJSON(ctx, http.MethodPut, workspacePath(postID)+"/agent-control", token,
		map[string]string{"mode": mode}, &control)

	return control, err
}

func (c *Client) CreateAgentGrant(
	ctx context.Context,
	token string,
	postID int64,
	name string,
	permissions []string,
	lifetime time.Duration,
) (AgentCredential, error) {
	credential := AgentCredential{APIURL: c.URL()}
	err := c.requestJSON(
		ctx,
		http.MethodPost,
		"/posts/"+strconv.FormatInt(postID, 10)+"/agents",
		token,
		map[string]any{
			"name":               name,
			"permissions":        permissions,
			"expires_in_seconds": int64(lifetime / time.Second),
		},
		&credential,
	)

	return credential, err
}

func (c *Client) AgentGrants(ctx context.Context, token string, postID int64) ([]AgentGrant, error) {
	var grants []AgentGrant

	err := c.request(
		ctx,
		http.MethodGet,
		"/posts/"+strconv.FormatInt(postID, 10)+"/agents",
		token,
		nil,
		&grants,
	)

	return grants, err
}

func (c *Client) RevokeAgentGrant(ctx context.Context, token, grantID string) (AgentGrant, error) {
	var grant AgentGrant

	err := c.request(ctx, http.MethodPost, "/agents/"+url.PathEscape(grantID)+"/revoke", token, nil, &grant)

	return grant, err
}
