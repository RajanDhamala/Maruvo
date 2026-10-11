package providers

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestConversationsPersistAcrossStoresAndKeepAccountBoundaries(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	scope := ChatScope{Profile: "worker", APIURL: "http://localhost:3000", Account: "7"}

	chat, err := NewConversation(t.TempDir(), "Fix the Go HTTP server", "deepseek", "deepseek-flash")
	if err != nil {
		t.Fatal(err)
	}

	chat.Messages = []ChatMessage{{"user", "Fix the HTTP server"}, {"assistant", "Use ServeMux."}}
	chat.Lines = []string{"You: Fix the HTTP server", "Agent: Use ServeMux."}
	chat.Draft = "Add a timeout too"

	chat.Usage = "Usage (API): input 10 · output 5"
	if err = SaveConversation(scope, chat); err != nil {
		t.Fatal(err)
	}

	loaded, err := LoadConversation(scope, chat.Directory, chat.ID)
	if err != nil || loaded.Draft != chat.Draft || len(loaded.Messages) != 2 || len(loaded.Lines) != 2 {
		t.Fatalf("restart: %+v %v", loaded, err)
	}

	older := chat
	chat.UpdatedAt = chat.UpdatedAt.Add(time.Second)

	chat.Messages = append(chat.Messages, ChatMessage{"user", "Add a timeout"})
	if err = SaveConversation(scope, chat); err != nil {
		t.Fatal(err)
	}

	if err = SaveConversation(scope, older); err != nil {
		t.Fatal(err)
	}

	loaded, err = LoadConversation(scope, chat.Directory, chat.ID)
	if err != nil || len(loaded.Messages) != 3 {
		t.Fatal("a stale background save overwrote the latest chat")
	}

	other, err := NewConversation(t.TempDir(), "Fix a different project", "openrouter", "example/model")
	if err != nil {
		t.Fatal(err)
	}

	if err = SaveConversation(scope, other); err != nil {
		t.Fatal(err)
	}

	list, err := ListConversations(scope)
	if err != nil || len(list) != 2 {
		t.Fatalf("cross-project picker: %v %v", list, err)
	}

	for _, different := range []ChatScope{
		{Profile: "requester", APIURL: scope.APIURL, Account: scope.Account},
		{Profile: scope.Profile, APIURL: scope.APIURL, Account: "8"},
		{Profile: scope.Profile, APIURL: "http://localhost:4000", Account: scope.Account},
	} {
		list, err = ListConversations(different)
		if err != nil || len(list) != 0 {
			t.Fatalf("cross-account chat exposure: %+v %v", different, err)
		}

		if _, err = LoadConversation(different, chat.Directory, chat.ID); err == nil {
			t.Fatal("another scope loaded the chat")
		}
	}

	root, err := chatRoot(scope)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()

	info, err := root.Lstat(chatHash(chat.Directory) + "/" + chat.ID + ".json")
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("chat permissions: %v %v", info, err)
	}
}

func TestChatStorageRejectsSymlinksTraversalAndOversizeSnapshots(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	scope := ChatScope{Profile: "worker", Account: "7"}

	chat, err := NewConversation(t.TempDir(), "Test", "deepseek", "deepseek-flash")
	if err != nil {
		t.Fatal(err)
	}

	if err = SaveConversation(scope, chat); err != nil {
		t.Fatal(err)
	}

	chat.Lines = []string{strings.Repeat("x", maxConversationBytes)}
	if err = SaveConversation(scope, chat); err == nil {
		t.Fatal("oversize chat was written")
	}

	chat.Lines = nil

	root, err := chatRoot(scope)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()

	name := chatHash(chat.Directory) + "/" + chat.ID + ".json"

	outside := filepath.Join(t.TempDir(), "outside.json")
	if err = os.WriteFile(outside, []byte("outside"), 0600); err != nil {
		t.Fatal(err)
	}

	if err = root.Remove(name); err != nil {
		t.Fatal(err)
	}

	if err = os.Symlink(outside, filepath.Join(root.Name(), name)); err != nil {
		t.Fatal(err)
	}

	if _, err = LoadConversation(scope, chat.Directory, chat.ID); err == nil {
		t.Fatal("followed a chat symlink")
	}

	if err = SaveConversation(scope, chat); err == nil {
		t.Fatal("replaced a chat symlink")
	}

	if _, err = LoadConversation(scope, chat.Directory, "../../outside"); err == nil {
		t.Fatal("allowed a traversal ID")
	}

	data, _ := os.ReadFile(outside)
	if string(data) != "outside" {
		t.Fatal("changed a file outside history")
	}
}

func TestResumedChatGetsFreshInstructionsWithoutReplayingTools(t *testing.T) {
	chat := Conversation{
		Messages: []ChatMessage{{"user", "Inspect the server"}, {"assistant", "It uses ServeMux."}},
	}
	client := mockClient(t, "deepseek", func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Messages []Message `json:"messages"`
		}
		if json.NewDecoder(r.Body).Decode(&request) != nil {
			t.Fatal("invalid request")
		}

		if len(request.Messages) != 4 || request.Messages[0].Role != "system" ||
			request.Messages[1].Content != "Inspect the server" || request.Messages[2].Content != "It uses ServeMux." ||
			request.Messages[3].Content != "Explain its timeouts" {
			t.Errorf("lost resume context: %+v", request.Messages)
		}

		for _, message := range request.Messages {
			if len(message.ToolCalls) > 0 || message.Role == "tool" || message.ReasoningContent != "" {
				t.Error("replayed an old tool or reasoning")
			}
		}

		io.WriteString(w, `{"choices":[{"message":{"role":"assistant","content":"Here are its timeouts."}}]}`)
	})

	_, err := client.RunAgent(
		t.Context(),
		t.TempDir(),
		ConversationHistory(chat),
		"Explain its timeouts",
		nil,
		func(Event) {},
	)
	if err != nil {
		t.Fatal(err)
	}
}
