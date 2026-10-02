package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
)

type DemoResponse struct {
	Message string `json:"message"`
	Service string `json:"service"`
}

func (c *Client) Demo(ctx context.Context, message string) (DemoResponse, error) {
	var result DemoResponse

	body, err := json.Marshal(map[string]string{"message": message})
	if err != nil {
		return result, err
	}

	if err := c.request(ctx, http.MethodPost, "/demo", "", bytes.NewReader(body), &result); err != nil {
		return result, err
	}

	if result.Message == "" || result.Service == "" {
		return result, fmt.Errorf("Go API returned an incomplete demo response")
	}

	return result, nil
}
