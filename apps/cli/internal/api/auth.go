package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
)

type User struct {
	ID       string `json:"id"`
	Username string `json:"username"`
	Email    string `json:"email"`
	Avatar   string `json:"avatar"`
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

func (c *Client) LoginURL() string {
	return c.baseURL + "/oauth/google"
}

func (c *Client) Me(ctx context.Context, token string) (User, error) {
	var user User
	if err := c.request(ctx, http.MethodGet, "/me", token, nil, &user); err != nil {
		return user, err
	}

	if user.Email == "" {
		return user, fmt.Errorf("Go API returned an incomplete user profile")
	}

	return user, nil
}
