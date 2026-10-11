package providers

import (
	"encoding/json"
	"io"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/zalando/go-keyring"
)

func TestReasoningCapabilities(t *testing.T) {
	for _, test := range []struct {
		name, provider, data string
		levels               []string
		budget               bool
	}{
		{"DeepSeek latest", "deepseek", `{"id":"deepseek-flash"}`, []string{"", "none", "low", "high", "max"}, false},
		{"DeepSeek pro", "deepseek", `{"id":"deepseek-v4-pro"}`, []string{"", "none", "low", "high", "max"}, false},
		{"mandatory thinking", "openrouter", `{"reasoning":{"supported_efforts":["high","low","none"],"mandatory":true}}`, []string{"", "low", "high"}, false},
		{"all gateway efforts", "openrouter", `{"reasoning":{"supported_efforts":null}}`, append([]string{""}, gatewayEfforts...), false},
		{"budget only", "openrouter", `{"reasoning":{"supports_max_tokens":true}}`, []string{""}, true},
		{"missing capabilities", "openrouter", `{}`, []string{""}, false},
		{"no efforts", "openrouter", `{"reasoning":{"supported_efforts":[]}}`, []string{""}, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			var model Model
			if err := json.Unmarshal([]byte(test.data), &model); err != nil {
				t.Fatal(err)
			}

			if got := model.ReasoningLevels(test.provider); !reflect.DeepEqual(got, test.levels) {
				t.Fatalf("levels: %v, want %v", got, test.levels)
			}

			if model.SupportsReasoningBudget(test.provider) != test.budget {
				t.Fatal("incorrect budget support")
			}
		})
	}
}

func TestModelOptionsRejectUnsupportedSettings(t *testing.T) {
	var model Model
	if err := json.Unmarshal(
		[]byte(
			`{"id":"test","reasoning":{"supported_efforts":["low","high"],"mandatory":true,"supports_max_tokens":true},"top_provider":{"max_completion_tokens":8192}}`,
		),
		&model,
	); err != nil {
		t.Fatal(err)
	}

	for _, options := range []Options{
		{Reasoning: "none", MaxTokens: 8192}, {Reasoning: "xhigh", MaxTokens: 8192},
		{MaxTokens: 16384}, {MaxTokens: -1}, {MaxTokens: 8192, ReasoningTokens: 8192},
		{Reasoning: "high", MaxTokens: 8192, ReasoningTokens: 1024},
	} {
		if model.ValidateOptions("openrouter", options) == nil {
			t.Fatalf("accepted invalid options: %+v", options)
		}
	}

	if err := model.ValidateOptions("openrouter", Options{Reasoning: "high", MaxTokens: 8192}); err != nil {
		t.Fatal(err)
	}

	if err := model.ValidateOptions(
		"openrouter",
		Options{ReasoningTokens: 2048, MaxTokens: 8192},
	); err != nil {
		t.Fatal(err)
	}

	if err := (Model{ID: "deepseek-flash"}).ValidateOptions(
		"deepseek",
		Options{Reasoning: "medium"},
	); err == nil {
		t.Fatal("DeepSeek's mapped medium must not be presented as an actual effort level")
	}
}

func TestReasoningPayloadAndToolHistory(t *testing.T) {
	for _, test := range []struct {
		provider                    string
		options                     Options
		thinking, effort, reasoning string
	}{
		{"deepseek", Options{}, "", "", ""},
		{"deepseek", Options{Reasoning: "none"}, `{"type":"disabled"}`, "", ""},
		{"deepseek", Options{Reasoning: "low"}, `{"type":"enabled"}`, `"low"`, ""},
		{"deepseek", Options{Reasoning: "high"}, `{"type":"enabled"}`, `"high"`, ""},
		{"deepseek", Options{Reasoning: "max", MaxTokens: 32768}, `{"type":"enabled"}`, `"max"`, ""},
		{"openrouter", Options{Reasoning: "high"}, "", "", `{"effort":"high"}`},
		{"openrouter", Options{ReasoningTokens: 2048}, "", "", `{"max_tokens":2048}`},
		{"openrouter", Options{}, "", "", ""},
	} {
		t.Run(test.provider+"/"+test.options.Reasoning+"/"+test.reasoning, func(t *testing.T) {
			calls := 0
			client := mockClient(t, test.provider, func(w http.ResponseWriter, r *http.Request) {
				calls++

				var payload map[string]json.RawMessage
				if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
					t.Fatal(err)
				}

				if string(payload["thinking"]) != test.thinking ||
					string(payload["reasoning_effort"]) != test.effort ||
					string(payload["reasoning"]) != test.reasoning {
					t.Errorf(
						"incorrect reasoning payload: %s / %s / %s",
						payload["thinking"],
						payload["reasoning_effort"],
						payload["reasoning"],
					)
				}

				var limit int

				_ = json.Unmarshal(payload["max_tokens"], &limit)
				if limit != test.options.OutputLimit() {
					t.Error("incorrect output limit")
				}

				if test.provider == "openrouter" &&
					string(payload["provider"]) != `{"require_parameters":true}` {
					t.Error("must require routing endpoints to support selected settings")
				}

				if calls == 1 {
					io.WriteString(
						w,
						`{"choices":[{"message":{"role":"assistant","content":"","reasoning_content":"keep-deepseek","reasoning":"keep-openrouter","reasoning_details":[{"type":"reasoning.encrypted","data":"keep-signed-block"}],"tool_calls":[{"id":"read-1","type":"function","function":{"name":"read_file","arguments":"{}"}}]},"finish_reason":"tool_calls"}]}`,
					)
				} else {
					var messages []Message
					if err := json.Unmarshal(payload["messages"], &messages); err != nil {
						t.Fatal(err)
					}

					assistant := messages[1]
					if assistant.ReasoningContent != "keep-deepseek" ||
						assistant.Reasoning != "keep-openrouter" ||
						string(
							assistant.ReasoningDetails,
						) != `[{"type":"reasoning.encrypted","data":"keep-signed-block"}]` {
						t.Error("tool follow-up must preserve provider reasoning fields")
					}

					io.WriteString(
						w,
						`{"choices":[{"message":{"role":"assistant","content":"Done"},"finish_reason":"stop"}]}`,
					)
				}
			})
			client.options = test.options
			history := []Message{{Role: "user", Content: "Read README"}}
			tools := []Tool{{Type: "function", Function: Function{Name: "read_file"}}}

			response, err := client.Complete(t.Context(), history, tools)
			if err != nil {
				t.Fatal(err)
			}

			history = append(
				history,
				response,
				Message{Role: "tool", ToolCallID: "read-1", Content: "README"},
			)
			if _, err := client.Complete(t.Context(), history, tools); err != nil {
				t.Fatal(err)
			}

			if calls != 2 {
				t.Fatal("expected a tool call and follow-up")
			}
		})
	}
}

func TestOptionsPersistWithoutChangingEncryptedCredential(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	keyring.MockInit()

	for _, profile := range []string{"requester", "worker"} {
		if err := SaveConnection(
			profile,
			"deepseek",
			"deepseek-flash",
			profile+"-secret",
			Options{Reasoning: "low", MaxTokens: 8192},
		); err != nil {
			t.Fatal(err)
		}
	}

	if err := Select(
		"worker",
		"deepseek",
		"deepseek-v4-pro",
		Options{Reasoning: "max", MaxTokens: 32768},
	); err != nil {
		t.Fatal(err)
	}

	for _, profile := range []string{"requester", "worker"} {
		key, connection, err := Credential(profile, "deepseek")
		if err != nil || key != profile+"-secret" {
			t.Fatalf("credential changed: %v", err)
		}

		wanted := "low"
		if profile == "worker" {
			wanted = "max"
		}

		if connection.Reasoning != wanted {
			t.Fatal("settings crossed profiles or did not persist")
		}

		client, err := ConnectedClient(profile, "deepseek")
		if err != nil || client.options != connection.Options {
			t.Fatalf("agent didn't use saved settings: %v", err)
		}

		var output strings.Builder
		if err := Commands(
			t.Context(),
			profile,
			[]string{"status"},
			strings.NewReader(""),
			&output,
			io.Discard,
		); err != nil {
			t.Fatal(err)
		}

		if strings.Contains(output.String(), key) || !strings.Contains(output.String(), wanted) {
			t.Fatal("status exposed a key or omitted settings")
		}
	}
}

func TestCurrentModelsAreDiscoveredAndOrdered(t *testing.T) {
	for _, provider := range Names {
		client := mockClient(t, provider, func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/key" {
				io.WriteString(w, `{"data":{}}`)
				return
			}

			io.WriteString(
				w,
				`{"data":[{"id":"deepseek-v4-pro","created":3},{"id":"deepseek-chat","created":1},{"id":"deepseek-flash","created":2},{"id":"author/newest","created":4,"supported_parameters":["tools"],"reasoning":{"supported_efforts":["low","high"],"mandatory":true}},{"id":"no-tools","created":5,"supported_parameters":["reasoning"]}]}`,
			)
		})

		models, err := client.Models(t.Context())
		if err != nil {
			t.Fatal(err)
		}

		if provider == "deepseek" && (models[0].ID != "deepseek-flash" || models[1].ID != "deepseek-v4-pro") {
			t.Fatal("current DeepSeek IDs must lead the returned catalog")
		}

		if provider == "openrouter" &&
			(models[0].ID != "author/newest" || len(models) != 4 || models[0].Reasoning == nil) {
			t.Fatal("OpenRouter must retain capabilities, sort newest first, and filter non-tool models")
		}
	}
}
