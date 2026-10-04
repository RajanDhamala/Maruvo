package tui

import (
	"encoding/json"
	"testing"

	"github.com/rajandhamala/Maruvo/cli/internal/api"
)

func TestWorkspaceStreamReplayPreservesSnapshot(t *testing.T) {
	m := model{
		workspace: api.Workspace{
			Cursor: "9-0",
			Post:   api.Post{ID: 1, Status: "in_progress"},
			State:  api.WorkspaceState{LastEventID: 7},
			Events: []api.WorkspaceEvent{
				{
					PostID:   1,
					ID:       7,
					StreamID: "10-0",
					Kind:     "message",
					Data:     json.RawMessage(`{"text":"saved"}`),
				},
			},
		},
	}

	m.applyWorkspaceEvent(api.WorkspaceEvent{
		PostID: 1, StreamID: "10-0", Kind: "message", Data: json.RawMessage(`{"text":"saved"}`),
	})

	if len(m.workspace.Events) != 1 || m.workspace.Cursor != "10-0" {
		t.Fatal("snapshot replay duplicated a message or failed to advance the stream cursor")
	}

	message := api.WorkspaceEvent{
		PostID: 1, StreamID: "10-1", Kind: "message", Data: json.RawMessage(`{"text":"live"}`),
	}
	m.applyWorkspaceEvent(message)
	m.applyWorkspaceEvent(message)

	if len(m.workspace.Events) != 2 || m.workspace.State.LastEventID != 7 {
		t.Fatal("Redis-first chat was dropped, duplicated, or changed the database cursor")
	}

	m.applyWorkspaceEvent(api.WorkspaceEvent{
		PostID: 1, ID: 6, StreamID: "10-2", Kind: "task.status",
		Data: json.RawMessage(`{"status":"negotiating"}`),
	})

	if m.workspace.Post.Status != "in_progress" || m.workspace.Cursor != "10-2" {
		t.Fatal("an old task event rewound the loaded database snapshot")
	}

	m.applyWorkspaceEvent(api.WorkspaceEvent{
		PostID: 1, ID: 8, StreamID: "10-10", Kind: "task.status",
		Data: json.RawMessage(`{"status":"completed"}`),
	})

	if m.workspace.Post.Status != "completed" || m.workspace.State.LastEventID != 8 {
		t.Fatal("a newer task event was not applied using numeric stream ordering")
	}
}
