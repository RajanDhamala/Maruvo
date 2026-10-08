package providers

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
)

type streamDelta struct {
	Message
	Calls []struct {
		Index    *int     `json:"index"`
		ID       string   `json:"id"`
		Type     string   `json:"type"`
		Function Function `json:"function"`
	} `json:"tool_calls"`
}

type completionChunk struct {
	Error   json.RawMessage `json:"error"`
	Usage   json.RawMessage `json:"usage"`
	Choices []struct {
		Index        int         `json:"index"`
		Delta        streamDelta `json:"delta"`
		FinishReason string      `json:"finish_reason"`
	} `json:"choices"`
}

func (c *Client) readCompletionStream(
	ctx context.Context,
	body io.Reader,
	emit func(Event),
) (Message, error) {
	message := Message{Role: "assistant"}
	finish := ""
	reasoningStarted := false
	toolsStarted := false

	var details []map[string]json.RawMessage

	limited := &io.LimitedReader{R: body, N: (8 << 20) + 1}
	scanner := bufio.NewScanner(limited)
	scanner.Buffer(make([]byte, 4096), 8<<20)

	var data strings.Builder

	emit(Event{Type: "response_start"})

	consume := func() (bool, error) {
		value := strings.TrimSpace(data.String())
		data.Reset()

		if value == "" {
			return false, nil
		}

		if value == "[DONE]" {
			return true, nil
		}

		var chunk completionChunk
		if json.Unmarshal([]byte(value), &chunk) != nil {
			return false, errors.New("invalid provider stream")
		}

		if len(chunk.Usage) != 0 && string(chunk.Usage) != "null" {
			var usage *Usage
			if json.Unmarshal(chunk.Usage, &usage) == nil {
				message.Usage = usage
			}
		}

		if len(chunk.Error) != 0 && string(chunk.Error) != "null" {
			return false, errors.New("provider could not complete this request")
		}

		for _, choice := range chunk.Choices {
			if choice.Index != 0 {
				continue
			}

			delta := choice.Delta
			if delta.Role != "" && delta.Role != "assistant" {
				return false, errors.New("provider returned no assistant response")
			}

			if finish != "" && (delta.Content != "" || len(delta.Calls) != 0 ||
				delta.ReasoningContent != "" || delta.Reasoning != "" || len(delta.ReasoningDetails) > 4) {
				return false, errors.New("provider continued a completed response")
			}

			message.Content += delta.Content
			message.ReasoningContent += delta.ReasoningContent
			message.Reasoning += delta.Reasoning

			thinking, err := mergeReasoningDetails(&details, delta.ReasoningDetails)
			if err != nil {
				return false, err
			}

			if !reasoningStarted &&
				(delta.ReasoningContent != "" || delta.Reasoning != "" || len(details) > 0) {
				reasoningStarted = true

				emit(Event{Type: "reasoning_start"})
			}

			switch {
			case delta.ReasoningContent != "":
				thinking = delta.ReasoningContent
			case delta.Reasoning != "":
				thinking = delta.Reasoning
			}

			if thinking != "" {
				emit(Event{Type: "reasoning_delta", Text: thinking})
			}

			if delta.Content != "" {
				emit(Event{Type: "assistant_delta", Text: delta.Content})
			}

			if len(delta.Calls) > 0 && !toolsStarted {
				toolsStarted = true

				emit(Event{Type: "tool_preparing"})
			}

			for _, part := range delta.Calls {
				if part.Index == nil || *part.Index < 0 || *part.Index >= 8 {
					return false, errors.New("invalid model tool call index")
				}

				for len(message.ToolCalls) <= *part.Index {
					message.ToolCalls = append(message.ToolCalls, ToolCall{})
				}

				call := &message.ToolCalls[*part.Index]

				call.ID += part.ID
				if part.Type != "" {
					call.Type = part.Type
				}

				call.Function.Name += part.Function.Name
				call.Function.Arguments += part.Function.Arguments
			}

			if choice.FinishReason != "" {
				finish = choice.FinishReason
			}
		}

		return false, nil
	}

	done := false

	for scanner.Scan() {
		if limited.N <= 0 {
			return message, errors.New("provider response is too large")
		}

		line := scanner.Text()
		if line == "" {
			var err error

			done, err = consume()
			if err != nil {
				return message, err
			}

			if done {
				break
			}
		} else if value, ok := strings.CutPrefix(line, "data:"); ok {
			if data.Len() > 0 {
				data.WriteByte('\n')
			}

			data.WriteString(strings.TrimPrefix(value, " "))
		}
	}

	if ctx.Err() != nil {
		return message, ctx.Err()
	}

	if scanner.Err() != nil {
		return message, errors.New("provider stream interrupted; response is incomplete")
	}

	if !done && data.Len() > 0 {
		var err error

		done, err = consume()
		if err != nil {
			return message, err
		}
	}

	if !done || finish == "" {
		return message, errors.New("provider stream interrupted; response is incomplete")
	}

	if finish == "length" {
		return message, errors.New(
			"model output limit reached; increase the output limit or lower reasoning in /model",
		)
	}

	if finish != "stop" && finish != "tool_calls" {
		return message, errors.New("provider could not complete this request")
	}

	if strings.TrimSpace(message.Content) == "" && len(message.ToolCalls) == 0 {
		return message, errors.New("provider returned an empty response")
	}

	for _, call := range message.ToolCalls {
		if call.ID == "" || call.Type != "function" || call.Function.Name == "" ||
			!json.Valid([]byte(call.Function.Arguments)) {
			return message, errors.New("invalid model tool call")
		}
	}

	if len(details) > 0 {
		message.ReasoningDetails, _ = json.Marshal(details)
	}

	c.redactCompletion(&message)

	return message, nil
}

func mergeReasoningDetails(details *[]map[string]json.RawMessage, raw json.RawMessage) (string, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return "", nil
	}

	var parts []map[string]json.RawMessage
	if json.Unmarshal(raw, &parts) != nil {
		return "", errors.New("invalid provider reasoning details")
	}

	var text strings.Builder

	for _, part := range parts {
		var kind string
		json.Unmarshal(part["type"], &kind)

		var target map[string]json.RawMessage

		for _, previous := range *details {
			if string(previous["type"]) == string(part["type"]) &&
				((len(part["index"]) > 0 && string(previous["index"]) == string(part["index"])) ||
					(len(part["index"]) == 0 && string(previous["id"]) == string(part["id"]))) {
				target = previous
				break
			}
		}

		if target == nil {
			if len(*details) >= 256 {
				return "", errors.New("provider returned too many reasoning blocks")
			}

			target = make(map[string]json.RawMessage)
			*details = append(*details, target)
		}

		for field, value := range part {
			if string(value) == "null" {
				continue
			}

			if field == "text" || field == "summary" || field == "data" || field == "signature" {
				var old, addition string
				if json.Unmarshal(value, &addition) != nil {
					return "", errors.New("invalid provider reasoning text")
				}

				json.Unmarshal(target[field], &old)

				target[field], _ = json.Marshal(old + addition)
				if (kind == "reasoning.text" && field == "text") ||
					(kind == "reasoning.summary" && field == "summary") {
					text.WriteString(addition)
				}
			} else {
				target[field] = value
			}
		}
	}

	return text.String(), nil
}

type streamRedactor struct {
	pending string
	secrets map[string]string
}

func (r *streamRedactor) write(text string, final bool) string {
	r.pending += text
	for secret, replacement := range r.secrets {
		if secret != "" {
			r.pending = strings.ReplaceAll(r.pending, secret, replacement)
		}
	}

	hold := 0

	if !final {
		for secret := range r.secrets {
			for size := 1; size < len(secret) && size <= len(r.pending); size++ {
				if strings.HasSuffix(r.pending, secret[:size]) {
					hold = max(hold, size)
				}
			}
		}
	}

	visible := r.pending[:len(r.pending)-hold]
	r.pending = r.pending[len(r.pending)-hold:]

	return visible
}
