package providers

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestUsageDisplaysOnlyReportedValues(t *testing.T) {
	for _, test := range []struct {
		name, provider, usage, want string
	}{
		{"DeepSeek", "deepseek", `{"prompt_tokens":123,"completion_tokens":45,"prompt_cache_hit_tokens":100,"prompt_cache_miss_tokens":23,"total_tokens":168}`, "Usage (API): input 123 · output 45 · cache hit 100 · cache miss 23 · total 168"},
		{"OpenRouter", "openrouter", `{"prompt_tokens":20,"completion_tokens":7,"prompt_tokens_details":{"cached_tokens":10,"cache_write_tokens":0},"completion_tokens_details":{"reasoning_tokens":4},"cost":0.000123456789}`, "Usage (API): input 20 · output 7 · cache hit 10 · cache write 0 · reasoning 4 · cost 0.000123456789 credits"},
		{"partial", "deepseek", `{"prompt_tokens":123}`, "Usage (API): input 123"},
		{"reported zero", "openrouter", `{"prompt_tokens":0,"completion_tokens":0,"prompt_tokens_details":{"cached_tokens":0},"cost":0}`, "Usage (API): input 0 · output 0 · cache hit 0 · cost 0 credits"},
		{"null fields", "deepseek", `{"prompt_tokens":null,"completion_tokens":null,"cost":null}`, ""},
		{"unknown fields", "openrouter", `{"unknown_count":500}`, ""},
		{"negative count", "deepseek", `{"prompt_tokens":-1,"completion_tokens":2}`, "Usage (API): output 2"},
		{"provider total", "deepseek", `{"prompt_tokens":3,"completion_tokens":4,"total_tokens":8}`, "Usage (API): input 3 · output 4 · total 8"},
	} {
		t.Run(test.name, func(t *testing.T) {
			client := mockClient(t, test.provider, func(w http.ResponseWriter, _ *http.Request) {
				io.WriteString(
					w,
					`{"choices":[{"message":{"role":"assistant","content":"Done"},"finish_reason":"stop"}],"usage":`+test.usage+`}`,
				)
			})

			message, err := client.Complete(t.Context(), nil, nil)
			if err != nil {
				t.Fatal(err)
			}

			if got := message.Usage.Summary(test.provider); got != test.want {
				t.Fatalf("usage: %q, want %q", got, test.want)
			}

			data, err := json.Marshal(message)
			if err != nil || strings.Contains(string(data), `"usage"`) {
				t.Fatal("usage must not be echoed to the provider as conversation content")
			}
		})
	}
}

func TestAbsentOrInvalidUsageDoesNotInventStats(t *testing.T) {
	for _, field := range []string{"", `,"usage":null`, `,"usage":{}`, `,"usage":{"prompt_tokens":"bad"}`} {
		client := mockClient(t, "deepseek", func(w http.ResponseWriter, _ *http.Request) {
			io.WriteString(
				w,
				`{"choices":[{"message":{"role":"assistant","content":"Done"},"finish_reason":"stop"}]`+field+`}`,
			)
		})

		var events []Event

		history, err := client.RunAgent(t.Context(), t.TempDir(), nil, "Hello", nil, func(event Event) {
			events = append(events, event)
		})
		if err != nil || len(history) != 3 || len(events) != 1 || events[0].Type != "assistant" {
			t.Fatalf("unreported usage must stay absent without breaking the answer: %v %v", events, err)
		}
	}
}

func TestUsageReportedForEveryToolRequestAndOutputLimit(t *testing.T) {
	for _, limited := range []bool{false, true} {
		requests := 0
		client := mockClient(t, "deepseek", func(w http.ResponseWriter, r *http.Request) {
			requests++

			var payload struct {
				Messages []json.RawMessage `json:"messages"`
			}
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				t.Error(err)
			}

			for _, message := range payload.Messages {
				if strings.Contains(string(message), `"usage"`) {
					t.Error("usage leaked into conversation payload")
				}
			}

			if requests == 1 {
				io.WriteString(
					w,
					`{"choices":[{"message":{"role":"assistant","content":"","tool_calls":[{"id":"read-1","type":"function","function":{"name":"read_file","arguments":"{\"path\":\"README.md\"}"}}]},"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":20,"completion_tokens":5}}`,
				)

				return
			}

			finish := "stop"
			if limited {
				finish = "length"
			}

			io.WriteString(
				w,
				`{"choices":[{"message":{"role":"assistant","content":"Done"},"finish_reason":"`+finish+`"}],"usage":{"prompt_tokens":30,"completion_tokens":7,"prompt_cache_hit_tokens":20}}`,
			)
		})

		directory := t.TempDir()
		if err := os.WriteFile(filepath.Join(directory, "README.md"), []byte("Read me"), 0600); err != nil {
			t.Fatal(err)
		}

		var usage []string

		_, err := client.RunAgent(t.Context(), directory, nil, "Read README", nil, func(event Event) {
			if event.Type == "usage" {
				usage = append(usage, event.Text)
			}
		})
		if (err != nil) != limited || requests != 2 || len(usage) != 2 {
			t.Fatalf("tool/limit usage: %v %v", usage, err)
		}

		if usage[0] != "Usage (API): input 20 · output 5" ||
			usage[1] != "Usage (API): input 30 · output 7 · cache hit 20" {
			t.Fatal("each request must show only its reported usage")
		}
	}
}
