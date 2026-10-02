package api

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

type Client struct {
	baseURL string
	http    *http.Client
}

type Error struct {
	StatusCode int
	Message    string
}

func (e *Error) Error() string {
	return e.Message
}

func NewClient(baseURL string) *Client {
	return &Client{
		baseURL: strings.TrimRight(baseURL, "/"),
		http:    &http.Client{Timeout: 20 * time.Second},
	}
}

func (c *Client) URL() string {
	return c.baseURL
}

func (c *Client) request(ctx context.Context, method, path, token string, body io.Reader, result any) error {
	return c.requestContent(ctx, method, path, token, "application/json", body, result)
}

func (c *Client) requestContent(
	ctx context.Context,
	method, path, token, contentType string,
	body io.Reader,
	result any,
) error {
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, body)
	if err != nil {
		return err
	}

	if body != nil {
		req.Header.Set("Content-Type", contentType)
	}

	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	response, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("call Go API: %w", err)
	}
	defer response.Body.Close()

	if response.StatusCode < 200 || response.StatusCode >= 300 {
		detail, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
		message := strings.TrimSpace(string(detail))

		var failure struct {
			Error string `json:"error"`
		}
		if json.Unmarshal(detail, &failure) == nil && failure.Error != "" {
			message = failure.Error
		}

		if message == "" {
			message = http.StatusText(response.StatusCode)
		}

		return &Error{StatusCode: response.StatusCode, Message: message}
	}

	if result == nil {
		return nil
	}

	if err := json.NewDecoder(io.LimitReader(response.Body, 4<<20)).Decode(result); err != nil {
		return fmt.Errorf("decode API response: %w", err)
	}

	return nil
}
