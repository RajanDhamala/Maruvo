package providers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func sendChunk(w http.ResponseWriter, delta any, finish string, usage any) {
	data, _ := json.Marshal(map[string]any{
		"choices": []any{map[string]any{"index": 0, "delta": delta, "finish_reason": finish}},
		"usage":   usage,
	})
	fmt.Fprintf(w, "data: %s\n\n", data)
}

func TestAgentStreamsBeforeCompletionAndRedactsSplitCredentials(t *testing.T) {
	for _, provider := range Names {
		t.Run(provider, func(t *testing.T) {
			finish := make(chan struct{})
			client := mockClient(t, provider, func(w http.ResponseWriter, r *http.Request) {
				var payload struct {
					Stream        bool `json:"stream"`
					StreamOptions struct {
						Usage bool `json:"include_usage"`
					} `json:"stream_options"`
				}
				if json.NewDecoder(r.Body).Decode(&payload) != nil || !payload.Stream ||
					!payload.StreamOptions.Usage {
					t.Error("agent must request streaming and reported usage")
				}

				w.Header().Set("Content-Type", "text/event-stream")
				io.WriteString(w, ": heartbeat\n\n")
				sendChunk(w, map[string]string{"reasoning_content": "Check test-"}, "", nil)
				sendChunk(w, map[string]string{"reasoning_content": "secret and owner-"}, "", nil)
				sendChunk(w, map[string]string{"reasoning_content": "session."}, "", nil)
				sendChunk(w, map[string]string{"content": "Hello "}, "", nil)
				w.(http.Flusher).Flush()

				select {
				case <-finish:
				case <-r.Context().Done():
					return
				}

				for _, part := range []string{"test-", "secret owner-", "session."} {
					sendChunk(w, map[string]string{"content": part}, "", nil)
				}

				usage := map[string]any{"prompt_tokens": 20, "completion_tokens": 7}

				if provider == "openrouter" {
					sendChunk(w, map[string]string{}, "stop", nil)

					data, _ := json.Marshal(map[string]any{"choices": []any{}, "usage": usage})
					fmt.Fprintf(w, "data: %s\n\n", data)
				} else {
					sendChunk(w, map[string]string{}, "stop", usage)
				}

				io.WriteString(w, "data: [DONE]\n\n")
			})

			ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
			defer cancel()

			early := make(chan string, 1)

			type result struct {
				history []Message
				events  []Event
				err     error
			}

			completed := make(chan result, 1)
			directory := t.TempDir()

			go func() {
				var answer result

				answer.history, answer.err = client.RunAgent(
					ctx,
					directory,
					nil,
					"Hello",
					nil,
					func(event Event) {
						answer.events = append(answer.events, event)
						if event.Type == "assistant_delta" && event.Text == "Hello " {
							early <- event.Text
						}
					},
					&Marketplace{token: "owner-session"},
				)
				completed <- answer
			}()

			select {
			case <-early:
			case <-ctx.Done():
				close(finish)
				t.Fatal("first text was buffered until the response completed")
			}

			select {
			case <-completed:
				close(finish)
				t.Fatal("request completed before the server finished")
			default:
			}

			close(finish)

			answer := <-completed
			if answer.err != nil || len(answer.history) != 3 {
				t.Fatalf("completion: %v", answer.err)
			}

			text, reasoning, final, usage := "", "", 0, 0

			for _, event := range answer.events {
				if strings.Contains(event.Text, "test-secret") ||
					strings.Contains(event.Text, "owner-session") {
					t.Fatal("credentials leaked through streamed events")
				}

				switch event.Type {
				case "assistant_delta":
					text += event.Text
				case "reasoning_delta":
					reasoning += event.Text
				case "assistant":
					final++

					if event.Text != "Hello [API key redacted] [login token redacted]." {
						t.Errorf("wrong final response: %q", event.Text)
					}
				case "usage":
					usage++
				}
			}

			if text != answer.history[2].Content || final != 1 || usage != 1 ||
				reasoning != "Check [API key redacted] and [login token redacted]." {
				t.Fatalf("stream assembly: %q / %q, final=%d usage=%d", text, reasoning, final, usage)
			}
		})
	}
}

func TestStreamedToolsPreserveReasoningAndRequireApproval(t *testing.T) {
	directory := t.TempDir()
	if err := os.WriteFile(filepath.Join(directory, "README.md"), []byte("Brief"), 0600); err != nil {
		t.Fatal(err)
	}

	requests := 0
	client := mockClient(t, "openrouter", func(w http.ResponseWriter, r *http.Request) {
		requests++

		var payload struct {
			Messages []Message `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Error(err)
		}

		w.Header().Set("Content-Type", "text/event-stream")

		if requests == 1 {
			for _, part := range []string{"Inspect ", "brief"} {
				sendChunk(w, map[string]any{
					"reasoning_content": part,
					"reasoning":         part,
					"reasoning_details": []any{
						map[string]any{
							"type":      "reasoning.text",
							"index":     0,
							"id":        "thought",
							"text":      part,
							"signature": part,
						},
						map[string]any{
							"type":  "reasoning.encrypted",
							"index": 1,
							"id":    "signed",
							"data":  part,
						},
					},
				}, "", nil)
			}

			sendChunk(w, map[string]any{"tool_calls": []any{
				map[string]any{
					"index":    1,
					"id":       "write",
					"type":     "function",
					"function": map[string]string{"name": "write_file", "arguments": `{"path":"result.md",`},
				},
				map[string]any{
					"index":    0,
					"id":       "read",
					"type":     "function",
					"function": map[string]string{"name": "read_file", "arguments": `{"path":"READ`},
				},
			}}, "", nil)
			sendChunk(w, map[string]any{"tool_calls": []any{
				map[string]any{"index": 0, "function": map[string]string{"arguments": `ME.md"}`}},
				map[string]any{"index": 1, "function": map[string]string{"arguments": `"content":"Hello"}`}},
			}}, "tool_calls", nil)
		} else {
			if len(payload.Messages) != 5 || payload.Messages[3].Content != "Brief" ||
				payload.Messages[4].Content != "Saved result.md" {
				t.Error("fragmented tool calls were not assembled and executed in index order")
			}

			message := payload.Messages[2]

			var details []map[string]any
			if json.Unmarshal(message.ReasoningDetails, &details) != nil || len(details) != 2 ||
				message.ReasoningContent != "Inspect brief" || message.Reasoning != "Inspect brief" ||
				details[0]["text"] != "Inspect brief" || details[0]["signature"] != "Inspect brief" ||
				details[1]["data"] != "Inspect brief" {
				t.Error("follow-up lost provider reasoning or signed details")
			}

			sendChunk(w, map[string]string{"content": "Ready"}, "stop", nil)
		}

		io.WriteString(w, "data: [DONE]\n\n")
	})
	approved := false
	_, err := client.RunAgent(
		t.Context(),
		directory,
		nil,
		"Prepare result",
		func(_ context.Context, approval Approval) (bool, error) {
			approved = approval.Path == "result.md" && approval.Content == "Hello"
			return approved, nil
		},
		func(Event) {},
	)

	content, readErr := os.ReadFile(filepath.Join(directory, "result.md"))
	if err != nil || !approved || requests != 2 || readErr != nil || string(content) != "Hello" {
		t.Fatalf("streamed tool workflow: %v, approved=%v, content=%q", err, approved, content)
	}
}

func TestIncompleteStreamNeverExecutesTools(t *testing.T) {
	for _, ending := range []string{"", "data: [DONE]\n\n", "data: {bad json}\n\n",
		"data: {\"error\":{\"message\":\"test-secret\"}}\n\n"} {
		client := mockClient(t, "deepseek", func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "text/event-stream")
			sendChunk(w, map[string]any{"tool_calls": []any{
				map[string]any{
					"index": 0,
					"id":    "write",
					"type":  "function",
					"function": map[string]string{
						"name":      "write_file",
						"arguments": `{"path":"result.md","content":"bad"}`,
					},
				},
			}}, "", nil)
			io.WriteString(w, ending)
		})
		directory := t.TempDir()

		_, err := client.RunAgent(
			t.Context(),
			directory,
			nil,
			"Write result",
			func(context.Context, Approval) (bool, error) {
				t.Error("incomplete stream reached file approval")
				return true, nil
			},
			func(Event) {},
		)
		if err == nil || strings.Contains(err.Error(), "test-secret") {
			t.Fatalf("invalid stream accepted or error leaked credentials: %v", err)
		}

		if _, err = os.Stat(filepath.Join(directory, "result.md")); !errors.Is(err, os.ErrNotExist) {
			t.Fatal("incomplete stream wrote a file")
		}
	}
}

func TestStreamCancellationAndOutputLimit(t *testing.T) {
	client := mockClient(t, "deepseek", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		sendChunk(w, map[string]string{"content": "Partial"}, "", nil)
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	})

	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()

	_, err := client.RunAgent(ctx, t.TempDir(), nil, "Hello", nil, func(event Event) {
		if event.Type == "assistant_delta" {
			cancel()
		}
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("stream cancellation: %v", err)
	}

	client = mockClient(t, "deepseek", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		sendChunk(
			w,
			map[string]string{"content": "Partial"},
			"length",
			map[string]int{"completion_tokens": 10},
		)
		io.WriteString(w, "data: [DONE]\n\n")
	})

	var usage string

	_, err = client.RunAgent(t.Context(), t.TempDir(), nil, "Hello", nil, func(event Event) {
		if event.Type == "assistant" {
			t.Error("output-limited answer must not be finalized")
		}

		if event.Type == "usage" {
			usage = event.Text
		}
	})
	if err == nil || !strings.Contains(err.Error(), "output limit") || usage != "Usage (API): output 10" {
		t.Fatalf("limited stream must retain reported usage: %q %v", usage, err)
	}
}

func TestStreamFramingAndUnreadableReasoning(t *testing.T) {
	stream := ": comment\r\n\r\nevent: message\r\ndata: {\"choices\":\r\n" +
		`data: [{"delta":{"reasoning_details":[{"type":"reasoning.encrypted","data":"opaque","index":0}]}}]}` +
		"\r\n\r\ndata: {\"choices\":[{\"delta\":{\"content\":\"Ready\"},\"finish_reason\":\"stop\"}]}\r\n\r\n" +
		"data: [DONE]"
	client := &Client{key: "test-secret"}
	thinking := false

	message, err := client.readCompletionStream(t.Context(), strings.NewReader(stream), func(event Event) {
		thinking = thinking || event.Type == "reasoning_start"
		if strings.Contains(event.Text, "opaque") {
			t.Error("encrypted provider details must not be displayed as reasoning text")
		}
	})
	if err != nil || !thinking || message.Content != "Ready" || len(message.ReasoningDetails) == 0 {
		t.Fatalf("SSE framing/reasoning: %+v %v", message, err)
	}
}
