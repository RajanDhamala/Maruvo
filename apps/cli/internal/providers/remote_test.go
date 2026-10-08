package providers

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gorilla/websocket"
	"github.com/rajandhamala/Maruvo/cli/internal/api"
)

func TestRemoteToolsMessagingAndFileIntegrity(t *testing.T) {
	data := []byte("remote result\n")
	hash := sha256.Sum256(data)
	file := api.WorkspaceFile{
		ID:      "file-1",
		PostID:  7,
		Name:    "result.txt",
		Size:    int64(len(data)),
		SHA256:  hex.EncodeToString(hash[:]),
		Purpose: "output",
	}

	var messages, uploads, downloads int

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer scoped-secret" {
			t.Error("wrong remote credential")
		}

		switch r.URL.Path {
		case "/posts/7/history":
			if r.URL.Query().Get("limit") != "5" || r.URL.Query().Get("before") != "10" {
				t.Error("history pagination lost")
			}

			_ = json.NewEncoder(w).
				Encode(api.TaskHistory{PostID: 7, NextBefore: 5, HasMore: true, Events: []api.WorkspaceEvent{{ID: 9, Kind: "message"}}})
		case "/posts/7/workspace":
			events := make([]api.WorkspaceEvent, 15)
			for i := range events {
				events[i] = api.WorkspaceEvent{
					ID:   int64(i + 1),
					Kind: "message",
					Data: json.RawMessage(`{"text":"earlier task message"}`),
				}
			}

			_ = json.NewEncoder(w).
				Encode(api.Workspace{Post: api.Post{ID: 7}, Escrow: api.Escrow{Transaction: "private-tx"}, Files: []api.WorkspaceFile{file}, Events: events, State: api.WorkspaceState{ReviewState: "submitted", SubmissionVersion: 1}})
		case "/posts/7/messages":
			var payload struct {
				Text      string `json:"text"`
				MessageID string `json:"message_id"`
			}
			if json.NewDecoder(r.Body).Decode(&payload) != nil || payload.MessageID != "same-message-1" {
				t.Error("missing stable message id")
			}

			messages++
			_ = json.NewEncoder(w).Encode(api.MessageReceipt{MessageID: payload.MessageID, StreamID: "1-0"})
		case "/ws/files":
			upgrader := websocket.Upgrader{}

			conn, err := upgrader.Upgrade(w, r, nil)
			if err != nil {
				t.Error(err)
				return
			}
			defer conn.Close()

			var control struct {
				Action  string `json:"action"`
				ID      string `json:"id"`
				Name    string `json:"name"`
				SHA256  string `json:"sha256"`
				Purpose string `json:"purpose"`
				Size    int64  `json:"size"`
			}
			if conn.ReadJSON(&control) != nil {
				t.Error("missing file action")
				return
			}

			if control.Action == "upload" {
				_, chunk, err := conn.ReadMessage()
				if err != nil || string(chunk) != string(data) || control.SHA256 != file.SHA256 ||
					control.Purpose != "input" {
					t.Error("invalid uploaded file")
				}

				uploads++
				_ = conn.WriteJSON(api.StreamFrame{Event: "file.complete", Data: mustJSON(t, file)})
			} else {
				if control.Action != "download" || control.ID != file.ID {
					t.Error("wrong download")
				}

				downloads++
				_ = conn.WriteJSON(api.StreamFrame{Event: "file.meta", Data: mustJSON(t, file)})
				_ = conn.WriteMessage(websocket.BinaryMessage, data)
				_ = conn.WriteJSON(api.StreamFrame{Event: "file.complete", Data: mustJSON(t, file)})
			}
		default:
			t.Errorf("unexpected route %s", r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	defer server.Close()

	market := &Marketplace{client: api.NewClient(server.URL), token: "scoped-secret", scoped: true}

	directory := t.TempDir()
	if err := os.WriteFile(filepath.Join(directory, "result.txt"), data, 0600); err != nil {
		t.Fatal(err)
	}

	root, err := os.OpenRoot(directory)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()

	approve := func(_ context.Context, change Approval) (bool, error) {
		if change.Action == "" {
			t.Error("file transfer has no approval action")
		}

		return true, nil
	}
	access := remoteFiles{root, approve}

	page, err := market.execute(t.Context(), marketCall("get_history", `{"post":7,"before":10,"limit":5}`))
	if err != nil || !strings.Contains(page, `"next_before":5`) {
		t.Fatal("history continuation lost", err)
	}

	snapshot, err := market.execute(t.Context(), marketCall("get_workspace", `{"post":7}`))
	if err != nil || strings.Contains(snapshot, "private-tx") ||
		!strings.Contains(snapshot, `"history_required":true`) {
		t.Fatal("workspace exposed transaction", err)
	}

	if _, err = market.execute(
		t.Context(),
		marketCall("send_message", `{"post":7,"text":"Use numeric sorting","message_id":"same-message-1"}`),
	); err != nil {
		t.Fatal(err)
	}

	if _, err = market.execute(
		t.Context(),
		marketCall("send_file", `{"post":7,"path":"result.txt","purpose":"input"}`),
		access,
	); err != nil {
		t.Fatal(err)
	}

	if _, err = market.execute(
		t.Context(),
		marketCall("receive_file", `{"post":7,"file_id":"file-1","path":"remote/result.txt"}`),
		access,
	); err != nil {
		t.Fatal(err)
	}

	received, err := root.ReadFile("remote/result.txt")
	if err != nil || string(received) != string(data) {
		t.Fatal("download did not preserve bytes", err)
	}

	if _, err = market.execute(
		t.Context(),
		marketCall("receive_file", `{"post":7,"file_id":"file-1","path":"remote/result.txt"}`),
		access,
	); err == nil {
		t.Fatal("download overwrote a local file")
	}

	for _, name := range []string{"../secret.txt", ".env", "link.txt"} {
		if name == "link.txt" {
			if err := os.Symlink(
				filepath.Join(directory, "result.txt"),
				filepath.Join(directory, name),
			); err != nil {
				t.Fatal(err)
			}
		}

		args, _ := json.Marshal(map[string]any{"post": 7, "path": name, "purpose": "input"})
		if _, err = market.execute(t.Context(), marketCall("send_file", string(args)), access); err == nil {
			t.Fatal("shared unsafe path", name)
		}
	}

	if messages != 1 || uploads != 1 || downloads != 1 {
		t.Fatalf("unexpected transfers: %d/%d/%d", messages, uploads, downloads)
	}

	if _, err = market.execute(
		t.Context(),
		marketCall("send_message", `{"post":7,"text":"duplicate without ID"}`),
	); err == nil {
		t.Fatal("message sent without stable ID")
	}

	if _, err = market.execute(
		t.Context(),
		marketCall("submit_delivery", `{"post":7,"note":"result"}`),
	); err == nil {
		t.Fatal("delivery missing version reached API")
	}
}

func mustJSON(t *testing.T, value any) json.RawMessage {
	t.Helper()

	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}

	return data
}
