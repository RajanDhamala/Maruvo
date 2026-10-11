package providers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rajandhamala/Maruvo/cli/internal/api"
)

func TestWorkspaceModelReceivesRoleContextAndExecutesApprovedEdit(t *testing.T) {
	worker := int64(1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/posts/4/workspace" {
			t.Errorf("unexpected task endpoint %s", r.URL.Path)
		}
		json.NewEncoder(w).Encode(api.Workspace{Post: api.Post{ID: 4, UserID: 3, AcceptedBy: &worker, Status: "in_progress"}, Escrow: api.Escrow{State: "confirmed"}})
	}))
	defer server.Close()
	market := &Marketplace{client: api.NewClient(server.URL), token: "session", TaskID: 4}
	market.SetAccount(api.User{ID: "1"})
	step := 0
	client := mockClient(t, "openrouter", func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Messages []Message `json:"messages"`
			Tools    []Tool    `json:"tools"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
		}
		if len(request.Messages) == 0 || !strings.Contains(request.Messages[0].Content, "Workspace loop:") || !strings.Contains(request.Messages[0].Content, `"user_id":"1"`) {
			t.Error("missing trusted task and account instructions")
		}
		for _, tool := range request.Tools {
			if tool.Function.Name == "find_posts" || tool.Function.Name == "accept_post" {
				t.Error("unrelated marketplace mutation exposed")
			}
		}
		message := Message{Role: "assistant"}
		switch step {
		case 0:
			message.ToolCalls = []ToolCall{marketCall("get_workspace", `{"post":4}`)}
		case 1:
			last := request.Messages[len(request.Messages)-1]
			if last.Role != "tool" || !strings.Contains(last.Content, "confirmed") {
				t.Error("missing fresh funding result")
			}
			message.ToolCalls = []ToolCall{fileCall("write_file", "result.md", "Task result")}
		case 2:
			message.Content = "Created result.md; test execution remains unavailable."
		default:
			t.Error("unexpected extra model turn")
		}
		step++
		json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": message}}})
	})
	directory := t.TempDir()
	approved := false
	_, err := client.RunAgent(t.Context(), directory, nil, "Continue task 4", func(_ context.Context, change Approval) (bool, error) {
		approved = change.Path == "result.md" && change.Content == "Task result"
		return approved, nil
	}, func(Event) {}, market)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(directory, "result.md"))
	if err != nil || !approved || string(data) != "Task result" || step != 3 {
		t.Fatalf("task edit failed: %v", err)
	}
}
