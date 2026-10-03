package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"time"
)

const MaxDescriptionBytes = 32 << 10
const MaxDescriptionCharacters = 12000

type PublicUser struct {
	ID          int64  `json:"id"`
	Username    string `json:"username"`
	Avatar      string `json:"avatar"`
	GitHubLogin string `json:"github_login"`
	GitHubURL   string `json:"github_url"`
}

type Post struct {
	Poster             *PublicUser `json:"poster"`
	Worker             *PublicUser `json:"worker"`
	Description        string      `json:"description"`
	AcceptanceCriteria string      `json:"acceptance_criteria"`
	InputFiles         []string    `json:"input_files"`
	ExpectedOutputs    []string    `json:"expected_outputs"`
	ID                 int64       `json:"id"`
	UserID             int64       `json:"user_id"`
	Title              string      `json:"title"`
	CostLamports       int64       `json:"cost_lamports"`
	EndTime            time.Time   `json:"end_time"`
	Status             string      `json:"status"`
	Level              string      `json:"level"`
	CreatedAt          time.Time   `json:"created_at"`
	UpdatedAt          time.Time   `json:"updated_at"`
	AcceptedBy         *int64      `json:"accepted_by"`
	AcceptedAt         *time.Time  `json:"accepted_at"`
	PosterWallet       string      `json:"poster_wallet"`
	WorkerWallet       string      `json:"worker_wallet"`
}

type CreatePostPayload struct {
	Description        string    `json:"description"`
	AcceptanceCriteria string    `json:"acceptance_criteria"`
	InputFiles         []string  `json:"input_files"`
	ExpectedOutputs    []string  `json:"expected_outputs"`
	Title              string    `json:"title"`
	CostLamports       int64     `json:"cost_lamports"`
	EndTime            time.Time `json:"end_time"`
	Level              string    `json:"level"`
}

func (c *Client) CreatePost(ctx context.Context, token string, payload CreatePostPayload) (Post, error) {
	var result struct {
		Post Post `json:"post"`
	}

	err := c.requestJSON(ctx, http.MethodPost, "/posts/create", token, payload, &result)

	return result.Post, err
}

func (c *Client) OwnPosts(ctx context.Context, token string) ([]Post, error) {
	var result struct {
		Posts []Post `json:"posts"`
	}

	err := c.request(ctx, http.MethodPost, "/posts/urs", token, nil, &result)

	return result.Posts, err
}

func (c *Client) Feed(ctx context.Context, token, level string) ([]Post, error) {
	var result struct {
		Posts []Post `json:"posts"`
	}

	err := c.requestJSON(
		ctx,
		http.MethodPost,
		"/posts/feed",
		token,
		map[string]string{"level": level},
		&result,
	)

	return result.Posts, err
}

func (c *Client) UpdateStatus(ctx context.Context, token string, id int64, status string) (Post, error) {
	var result struct {
		Post Post `json:"post"`
	}

	err := c.requestJSON(ctx, http.MethodPost, "/posts/status", token, struct {
		ID     int64  `json:"id"`
		Status string `json:"status"`
	}{ID: id, Status: status}, &result)

	return result.Post, err
}

func (c *Client) DeletePost(ctx context.Context, token string, id int64) error {
	return c.requestJSON(ctx, http.MethodDelete, "/posts/del", token, map[string]int64{"id": id}, nil)
}

func (c *Client) requestJSON(ctx context.Context, method, path, token string, payload, result any) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	return c.request(ctx, method, path, token, bytes.NewReader(body), result)
}
