package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

type User struct {
	AgentAccess     *AgentGrant `json:"agent_access,omitempty"`
	ID              string      `json:"id"`
	Username        string      `json:"username"`
	Email           string      `json:"email"`
	Avatar          string      `json:"avatar"`
	GitHubLogin     string      `json:"github_login"`
	GitHubURL       string      `json:"github_url"`
	GoogleConnected bool        `json:"google_connected"`
	GitHubConnected bool        `json:"github_connected"`
}

func (c *Client) ExchangeCLICode(ctx context.Context, code, verifier string) (string, error) {
	body, err := json.Marshal(map[string]string{"code": code, "code_verifier": verifier})
	if err != nil {
		return "", err
	}

	var response struct {
		Token string `json:"token"`
	}
	if err := c.request(
		ctx,
		http.MethodPost,
		"/oauth/cli/token",
		"",
		bytes.NewReader(body),
		&response,
	); err != nil {
		return "", err
	}

	if response.Token == "" {
		return "", fmt.Errorf("Go API returned no access token")
	}

	return response.Token, nil
}

func (c *Client) LoginURL(provider string) string {
	return c.baseURL + "/oauth/" + provider
}

func (c *Client) GitHubLinkURL(
	ctx context.Context,
	token, redirect, challenge, state string,
) (string, error) {
	var response struct {
		URL string `json:"url"`
	}

	err := c.requestJSON(ctx, http.MethodPost, "/oauth/github/link", token, map[string]string{
		"cli_redirect_uri": redirect, "code_challenge": challenge, "cli_state": state,
	}, &response)
	if err != nil {
		return "", err
	}

	if !strings.HasPrefix(response.URL, "/oauth/github?request=") {
		return "", fmt.Errorf("Go API returned an invalid GitHub connection URL")
	}

	return c.baseURL + response.URL, nil
}

func (c *Client) Me(ctx context.Context, token string) (User, error) {
	var user User
	if err := c.request(ctx, http.MethodGet, "/me", token, nil, &user); err != nil {
		return user, err
	}

	if user.ID == "" || user.Username == "" {
		return user, fmt.Errorf("Go API returned an incomplete user profile")
	}

	return user, nil
}
