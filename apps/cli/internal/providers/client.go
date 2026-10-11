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
	ID                  string            `json:"id"`
	Name                string            `json:"name,omitempty"`
	Created             int64             `json:"created,omitempty"`
	ContextLength       int               `json:"context_length,omitempty"`
	SupportedParameters []string          `json:"supported_parameters,omitempty"`
	Reasoning           *ReasoningSupport `json:"reasoning,omitempty"`
	TopProvider         struct {
		MaxCompletionTokens int `json:"max_completion_tokens,omitempty"`
	} `json:"top_provider,omitempty"`
}

func (m Model) DisplayName() string {
	if m.ID == "deepseek-flash" {
		return "DeepSeek V4.1 Flash"
	}

	if m.Name != "" {
		return m.Name
	}

	return m.ID
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
	Reasoning        string          `json:"reasoning,omitempty"`
	ReasoningDetails json.RawMessage `json:"reasoning_details,omitempty"`
	Usage            *Usage          `json:"-"`
}

type Client struct {
	provider, model, key, baseURL string
	http                          *http.Client
	options                       Options
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
			Timeout: 5 * time.Minute,
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return errors.New("provider redirects are not allowed")
			},
		},
	}, nil
}

func (c *Client) response(ctx context.Context, method, path string, payload any) (*http.Response, error) {
	var body io.Reader

	if payload != nil {
		data, err := json.Marshal(payload)
		if err != nil {
			return nil, err
		}

		body = bytes.NewReader(data)
	}

	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, body)
	if err != nil {
		return nil, errors.New("invalid provider request")
	}

	req.Header.Set("Authorization", "Bearer "+c.key)
	req.Header.Set("Content-Type", "application/json")

	if c.provider == "openrouter" {
		req.Header.Set("X-OpenRouter-Title", "Maruvo")
	}

	resp, err := c.http.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}

		return nil, fmt.Errorf("could not reach %s; check your connection and retry", c.provider)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		resp.Body.Close()

		switch resp.StatusCode {
		case 401, 403:
			return nil, errors.New("provider rejected the API key; reconnect the provider")
		case 402:
			return nil, errors.New("provider credits are exhausted")
		case 429:
			return nil, errors.New("provider rate limit reached; retry later")
		default:
			return nil, fmt.Errorf("%s request failed (HTTP %d)", c.provider, resp.StatusCode)
		}
	}

	return resp, nil
}

func (c *Client) request(ctx context.Context, method, path string, payload, result any) error {
	resp, err := c.response(ctx, method, path, payload)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

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
	slices.SortFunc(result.Data, func(a, b Model) int {
		if c.provider == "deepseek" {
			preferred := []string{"deepseek-flash", "deepseek-v4-pro"}

			aRank, bRank := slices.Index(preferred, a.ID), slices.Index(preferred, b.ID)
			if aRank < 0 {
				aRank = len(preferred)
			}

			if bRank < 0 {
				bRank = len(preferred)
			}

			if aRank != bRank {
				return aRank - bRank
			}
		}

		if a.Created > b.Created {
			return -1
		}

		if a.Created < b.Created {
			return 1
		}

		return strings.Compare(a.ID, b.ID)
	})

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
	return c.complete(ctx, messages, tools, nil)
}

func (c *Client) complete(
	ctx context.Context,
	messages []Message,
	tools []Tool,
	emit func(Event),
) (Message, error) {
	if !validModel(c.model) {
		return Message{}, errors.New("select a model before starting the agent")
	}

	payload := map[string]any{
		"model":      c.model,
		"messages":   messages,
		"stream":     emit != nil,
		"max_tokens": c.options.OutputLimit(),
	}
	if emit != nil {
		payload["stream_options"] = map[string]bool{"include_usage": true}
	}

	if len(tools) != 0 {
		payload["tools"], payload["tool_choice"] = tools, "auto"
	}

	if c.provider == "deepseek" && c.options.Reasoning != "" {
		if c.options.Reasoning == "none" {
			payload["thinking"] = map[string]string{"type": "disabled"}
		} else {
			payload["thinking"] = map[string]string{"type": "enabled"}
			payload["reasoning_effort"] = c.options.Reasoning
		}
	}

	if c.provider == "openrouter" {
		payload["provider"] = map[string]bool{"require_parameters": true}
		if c.options.Reasoning != "" {
			payload["reasoning"] = map[string]string{"effort": c.options.Reasoning}
		} else if c.options.ReasoningTokens > 0 {
			payload["reasoning"] = map[string]int{"max_tokens": c.options.ReasoningTokens}
		}
	}

	resp, err := c.response(ctx, http.MethodPost, "/chat/completions", payload)
	if err != nil {
		return Message{}, err
	}
	defer resp.Body.Close()

	if emit != nil && strings.HasPrefix(resp.Header.Get("Content-Type"), "text/event-stream") {
		return c.readCompletionStream(ctx, resp.Body, emit)
	}

	var response struct {
		Error   json.RawMessage `json:"error"`
		Usage   json.RawMessage `json:"usage"`
		Choices []struct {
			Message      Message `json:"message"`
			FinishReason string  `json:"finish_reason"`
		} `json:"choices"`
	}

	data, err := io.ReadAll(io.LimitReader(resp.Body, (8<<20)+1))
	if err != nil || len(data) > 8<<20 {
		return Message{}, errors.New("provider response is incomplete or too large")
	}

	if json.Unmarshal(data, &response) != nil {
		return Message{}, errors.New("invalid provider response")
	}

	var usage *Usage
	if json.Unmarshal(response.Usage, &usage) != nil {
		usage = nil
	}

	reported := Message{Usage: usage}

	if len(response.Error) != 0 && string(response.Error) != "null" {
		return reported, errors.New("provider could not complete this request")
	}

	if len(response.Choices) == 0 || response.Choices[0].Message.Role != "assistant" {
		return reported, errors.New("provider returned no assistant response")
	}

	choice := response.Choices[0]

	choice.Message.Usage = usage
	if choice.FinishReason == "length" {
		return reported, errors.New(
			"model output limit reached; increase the output limit or lower reasoning in /model",
		)
	}

	if strings.TrimSpace(choice.Message.Content) == "" && len(choice.Message.ToolCalls) == 0 {
		return reported, errors.New("provider returned an empty response")
	}

	c.redactCompletion(&choice.Message)

	return choice.Message, nil
}

func (c *Client) redactCompletion(message *Message) {
	message.Content = strings.ReplaceAll(message.Content, c.key, "[API key redacted]")
	for i := range message.ToolCalls {
		call := &message.ToolCalls[i]
		call.Function.Arguments = strings.ReplaceAll(call.Function.Arguments, c.key, "[API key redacted]")
		call.Function.Name = strings.ReplaceAll(call.Function.Name, c.key, "[API key redacted]")
	}
}
