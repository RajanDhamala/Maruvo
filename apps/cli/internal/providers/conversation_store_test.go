package providers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/rajandhamala/Maruvo/cli/internal/api"
)

func TestAccountChatStoreRestoresRemoteHistoryAndRetriesOfflineSaves(t *testing.T) {
	config := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", config)

	var (
		mu    sync.Mutex
		saved Conversation
	)

	unavailable := false

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()

		if r.Header.Get("Authorization") != "Bearer owner-token" || r.URL.Query().Get("profile") != "worker" {
			t.Error("chat request lost account/profile")
		}

		if unavailable {
			http.Error(w, "offline", 503)
			return
		}

		switch {
		case r.Method == http.MethodPut:
			var chat Conversation
			if json.NewDecoder(r.Body).Decode(&chat) != nil {
				t.Error("invalid snapshot")
			}

			if !chat.UpdatedAt.Before(saved.UpdatedAt) {
				saved = chat
			}

			json.NewEncoder(w).Encode(map[string]bool{"saved": true})
		case r.URL.Path == "/agent/chats":
			json.NewEncoder(w).Encode([]ConversationSummary{SummarizeConversation(saved)})
		default:
			json.NewEncoder(w).Encode(saved)
		}
	}))
	defer server.Close()

	scope := ChatScope{Profile: "worker", APIURL: server.URL, Account: "7"}
	store := ConversationStore{Scope: scope, Client: api.NewClient(server.URL), Token: "owner-token"}
	snapshot := func() Conversation { mu.Lock(); defer mu.Unlock(); return saved }
	setOffline := func(value bool) { mu.Lock(); defer mu.Unlock(); unavailable = value }

	chat, err := NewConversation(t.TempDir(), "Go server", "deepseek", "deepseek-flash")
	if err != nil {
		t.Fatal(err)
	}

	chat.Messages = []ChatMessage{
		{Role: "user", Content: "Write a server"},
		{Role: "assistant", Content: "Use ServeMux."},
	}

	chat.Draft = "Add timeouts"
	if err = store.Save(t.Context(), chat); err != nil {
		t.Fatal(err)
	}

	if len(snapshot().Messages) != 2 || snapshot().Draft != chat.Draft {
		t.Fatal("signed-in chat was not sent to database API")
	}

	if err = os.RemoveAll(filepath.Join(config, "maruvo", "profiles", "worker", "chats")); err != nil {
		t.Fatal(err)
	}

	list, err := store.List(t.Context())
	if err != nil || len(list) != 1 || list[0].ID != chat.ID {
		t.Fatalf("remote-only history missing: %v %v", list, err)
	}

	loaded, err := store.Load(t.Context(), chat.Directory, chat.ID)
	if err != nil || len(loaded.Messages) != 2 || loaded.Draft != chat.Draft {
		t.Fatalf("remote chat could not resume: %+v %v", loaded, err)
	}

	setOffline(true)

	loaded.Draft = "Keep my offline draft"

	loaded.UpdatedAt = loaded.UpdatedAt.Add(time.Second)
	if err = store.Save(t.Context(), loaded); err == nil {
		t.Fatal("failed database save was hidden")
	}

	fallback, err := store.Load(t.Context(), chat.Directory, chat.ID)
	if err != nil || fallback.Draft != loaded.Draft {
		t.Fatal("database outage lost the local draft")
	}

	setOffline(false)

	if _, err = store.List(t.Context()); err != nil || snapshot().Draft != loaded.Draft {
		t.Fatalf("local snapshot was not synced after reconnection: %v", err)
	}
}
