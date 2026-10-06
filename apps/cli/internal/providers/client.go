package providers

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strings"
	"time"
	"unicode"
)

var Names = []string{"deepseek", "openrouter"}

type Model struct {
	ID                  string   `json:"id"`
	SupportedParameters []string `json:"supported_parameters,omitempty"`
}

type Function struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Parameters  json.RawMessage `json:"parameters,omitempty"`
	Arguments   string          `json:"arguments,omitempty"`
}

type Tool struct {
	Type     string   `json:"type"`
	Function Function `json:"function"`
}

type ToolCall struct {
	ID       string   `json:"id"`
	Type     string   `json:"type"`
	Function Function `json:"function"`
}

type Message struct {
	Role             string          `json:"role"`
	Content          string          `json:"content"`
	ToolCalls        []ToolCall      `json:"tool_calls,omitempty"`
	ToolCallID       string          `json:"tool_call_id,omitempty"`
	ReasoningContent string          `json:"reasoning_content,omitempty"`
	ReasoningDetails json.RawMessage `json:"reasoning_details,omitempty"`
}

type Client struct {
	provider, model, key, baseURL string
	http                          *http.Client
}

func NewClient(provider, model, key string) (*Client, error) {
	var baseURL string

	switch provider {
	case "deepseek":
		baseURL = "https://api.deepseek.com"
	case "openrouter":
		baseURL = "https://openrouter.ai/api/v1"
	default:
		return nil, errors.New("choose deepseek or openrouter")
	}

	key = strings.TrimSpace(key)
	if key == "" || len(key) > 4096 || strings.ContainsFunc(key, func(r rune) bool {
		return unicode.IsSpace(r) || unicode.IsControl(r)
	}) {
		return nil, errors.New("enter a valid API key")
	}

	return &Client{
		provider: provider, model: model, key: key, baseURL: baseURL,
		http: &http.Client{
			Timeout: 2 * time.Minute,
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return errors.New("provider redirects are not allowed")
			},
		},
	}, nil
}

func (c *Client) request(ctx context.Context, method, path string, payload, result any) error {
	var body io.Reader

	if payload != nil {
		data, err := json.Marshal(payload)
		if err != nil {
			return err
		}

		body = bytes.NewReader(data)
	}

	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, body)
	if err != nil {
		return errors.New("invalid provider request")
	}

	req.Header.Set("Authorization", "Bearer "+c.key)
	req.Header.Set("Content-Type", "application/json")

	if c.provider == "openrouter" {
		req.Header.Set("X-OpenRouter-Title", "Maruvo")
	}

	resp, err := c.http.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}

		return fmt.Errorf("could not reach %s; check your connection and retry", c.provider)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		switch resp.StatusCode {
		case 401, 403:
			return errors.New("provider rejected the API key; reconnect the provider")
		case 402:
			return errors.New("provider credits are exhausted")
		case 429:
			return errors.New("provider rate limit reached; retry later")
		default:
			return fmt.Errorf("%s request failed (HTTP %d)", c.provider, resp.StatusCode)
		}
	}

	data, err := io.ReadAll(io.LimitReader(resp.Body, (8<<20)+1))
	if err != nil || len(data) > 8<<20 {
		return errors.New("provider response is incomplete or too large")
	}

	if json.Unmarshal(data, result) != nil {
		return errors.New("invalid provider response")
	}

	return nil
}

func (c *Client) Models(ctx context.Context) ([]Model, error) {
	if c.provider == "openrouter" {
		var key struct {
			Data json.RawMessage `json:"data"`
		}
		if err := c.request(ctx, http.MethodGet, "/key", nil, &key); err != nil {
			return nil, err
		}

		if len(key.Data) == 0 || string(key.Data) == "null" {
			return nil, errors.New("invalid provider key response")
		}
	}

	var result struct {
		Data []Model `json:"data"`
	}
	if err := c.request(ctx, http.MethodGet, "/models", nil, &result); err != nil {
		return nil, err
	}

	result.Data = slices.DeleteFunc(result.Data, func(m Model) bool {
		return !validModel(m.ID) || strings.Contains(m.ID, c.key) ||
			(c.provider == "openrouter" && m.SupportedParameters != nil &&
				!slices.Contains(m.SupportedParameters, "tools"))
	})
	slices.SortFunc(result.Data, func(a, b Model) int { return strings.Compare(a.ID, b.ID) })

	if len(result.Data) == 0 {
		return nil, errors.New("provider returned no available models")
	}

	return result.Data, nil
}

func validModel(model string) bool {
	return model != "" && len(model) <= 200 && !strings.ContainsFunc(model, func(r rune) bool {
		return unicode.IsSpace(r) || unicode.IsControl(r)
	})
}

func (c *Client) Complete(ctx context.Context, messages []Message, tools []Tool) (Message, error) {
	if !validModel(c.model) {
		return Message{}, errors.New("select a model before starting the agent")
	}

	payload := map[string]any{"model": c.model, "messages": messages, "stream": false, "max_tokens": 4096}
	if len(tools) != 0 {
		payload["tools"], payload["tool_choice"] = tools, "auto"
	}

	if c.provider == "deepseek" {
		payload["thinking"] = map[string]string{"type": "disabled"}
	}

	var response struct {
		Error   json.RawMessage `json:"error"`
		Choices []struct {
			Message      Message `json:"message"`
			FinishReason string  `json:"finish_reason"`
		} `json:"choices"`
	}
	if err := c.request(ctx, http.MethodPost, "/chat/completions", payload, &response); err != nil {
		return Message{}, err
	}

	if len(response.Error) != 0 && string(response.Error) != "null" {
		return Message{}, errors.New("provider could not complete this request")
	}

	if len(response.Choices) == 0 || response.Choices[0].Message.Role != "assistant" {
		return Message{}, errors.New("provider returned no assistant response")
	}

	choice := response.Choices[0]
	if choice.FinishReason == "length" {
		return Message{}, errors.New("model output limit reached; narrow the request and retry")
	}

	if strings.TrimSpace(choice.Message.Content) == "" && len(choice.Message.ToolCalls) == 0 {
		return Message{}, errors.New("provider returned an empty response")
	}

	choice.Message.Content = strings.ReplaceAll(choice.Message.Content, c.key, "[API key redacted]")
	for i := range choice.Message.ToolCalls {
		call := &choice.Message.ToolCalls[i]
		call.Function.Arguments = strings.ReplaceAll(call.Function.Arguments, c.key, "[API key redacted]")
		call.Function.Name = strings.ReplaceAll(call.Function.Name, c.key, "[API key redacted]")
	}

	return choice.Message, nil
}
