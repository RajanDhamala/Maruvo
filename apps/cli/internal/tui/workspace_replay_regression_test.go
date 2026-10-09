package tui

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
	"github.com/rajandhamala/Maruvo/cli/internal/api"
)

func replayMessage(actor int64, stream string, id int64, messageID, text string) api.WorkspaceEvent {
	data, _ := json.Marshal(map[string]string{"message_id": messageID, "text": text})
	return api.WorkspaceEvent{PostID: 5, ActorID: &actor, StreamID: stream, ID: id,
		Kind: "message", Data: data, CreatedAt: time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC)}
}

func assertReplayTextCount(t *testing.T, m model, text string, want int) {
	t.Helper()
	got := 0
	for _, row := range m.conversationRows(100) {
		if strings.TrimSpace(ansi.Strip(row.text)) == text {
			got++
		}
	}
	if got != want {
		t.Errorf("rendered %q %d times, want %d", text, got, want)
	}
}

func TestWorkspaceReplayLogicalMessageIdentity(t *testing.T) {
	for _, stream := range []string{"101-0", ""} {
		t.Run("archive_stream_"+stream, func(t *testing.T) {
			m := chatModel()
			m.workspace = api.Workspace{Post: api.Post{ID: 5}}
			live := replayMessage(1, "100-0", 0, "task5-first", "First delivery")
			m.applyWorkspaceEvent(live)
			archived := live
			archived.StreamID, archived.ID = stream, 41
			m.applyWorkspaceEvent(archived)
			m.applyWorkspaceEvent(archived)
			// Reusing the client ID from another actor is a different message.
			m.applyWorkspaceEvent(replayMessage(2, "102-0", 42, "task5-first", "Peer delivery"))
			// Identical text with a fresh client ID is also a different message.
			m.applyWorkspaceEvent(replayMessage(1, "103-0", 43, "task5-second", "First delivery"))
			assertReplayTextCount(t, m, "First delivery", 2)
			assertReplayTextCount(t, m, "Peer delivery", 1)
			if len(m.workspace.Events) != 3 {
				t.Errorf("got %d events, want three logical messages", len(m.workspace.Events))
			}
		})
	}
}

func TestWorkspaceReplayHydratesArchiveBehindStreamCursor(t *testing.T) {
	m := chatModel()
	m.workspace = api.Workspace{Post: api.Post{ID: 5}}
	live := replayMessage(1, "100-0", 0, "task5-first", "First delivery")
	m.applyWorkspaceEvent(live)
	m.applyWorkspaceEvent(replayMessage(2, "101-0", 0, "task5-peer", "Peer delivery"))
	archived := live
	archived.ID = 41
	m.applyWorkspaceEvent(archived)
	m.applyWorkspaceEvent(archived)
	if len(m.workspace.Events) != 2 {
		t.Fatalf("archive replay changed event count to %d", len(m.workspace.Events))
	}
	if m.workspace.Events[0].ID != 41 {
		t.Errorf("live message did not receive archived database ID: %+v", m.workspace.Events[0])
	}
	if m.workspace.Cursor != "101-0" {
		t.Errorf("archive hydration rewound stream cursor to %q", m.workspace.Cursor)
	}
	if m.workspace.State.LastEventID != 41 {
		t.Errorf("database cursor = %d, want 41", m.workspace.State.LastEventID)
	}
	assertReplayTextCount(t, m, "First delivery", 1)
	assertReplayTextCount(t, m, "Peer delivery", 1)
}

func TestWorkspaceReplayResnapshotPreservesUnsentComposer(t *testing.T) {
	m := chatModel()
	m.ctx = context.Background()
	m.posts = []api.Post{{ID: 5}}
	m.workspace = api.Workspace{Post: m.posts[0]}
	m.composer.root = "/tmp/task5-project"
	m.composer.draft = textField{value: "Unsent\nreview notes", cursor: 7, limit: 4000}
	m.composer.attachments = []localFile{{path: "/tmp/task5-project/patch.diff"}}
	want := m.composer
	m.applyWorkspaceEvent(replayMessage(1, "100-0", 0, "task5-first", "First delivery"))
	next, _ := m.workspaceConnected(workspaceConnected{generation: m.workspaceGen,
		err: &api.Error{StatusCode: 409, Message: "history required"}})
	m = next.(model)
	defer m.workspaceCancel()
	archived := replayMessage(1, "100-0", 41, "task5-first", "First delivery")
	snapshot := api.Workspace{Post: api.Post{ID: 5}, Cursor: "99-0",
		State: api.WorkspaceState{LastEventID: 41}, Events: []api.WorkspaceEvent{archived}}
	next, _ = m.workspaceLoaded(workspaceLoaded{generation: m.workspaceGen, workspace: snapshot})
	m = next.(model)
	m.applyWorkspaceEvent(archived)
	m.applyWorkspaceEvent(archived)
	m.applyWorkspaceEvent(replayMessage(2, "101-0", 0, "task5-peer", "Peer delivery"))
	// A previous connection's snapshot must not overwrite the current transcript.
	next, _ = m.workspaceLoaded(workspaceLoaded{generation: m.workspaceGen - 1,
		workspace: api.Workspace{Post: api.Post{ID: 99}}})
	m = next.(model)
	if !reflect.DeepEqual(m.composer, want) {
		t.Errorf("resnapshot changed unsent composer: got %+v, want %+v", m.composer, want)
	}
	if m.workspace.Post.ID != 5 {
		t.Errorf("stale snapshot replaced task with %d", m.workspace.Post.ID)
	}
	assertReplayTextCount(t, m, "First delivery", 1)
	assertReplayTextCount(t, m, "Peer delivery", 1)
}

func TestWorkspaceReplayDoesNotWakeAgentForDuplicateTransport(t *testing.T) {
	m := chatModel()
	m.workspace = api.Workspace{Post: api.Post{ID: 5, Status: "in_progress"}}
	m.workAgent = workspaceAgent{active: true, postID: 5}
	original := replayMessage(3, "100-0", 0, "requester-question", "Please verify the patch")
	m.applyWorkspaceEvent(original)
	replay := original
	replay.StreamID = "101-0"
	replay.ID = 41
	frame, _ := json.Marshal(replay)
	next, _ := m.workspaceFrame(workspaceFrame{generation: m.workspaceGen, frame: api.StreamFrame{Event: "workspace.event", Data: frame}})
	m = next.(model)
	if m.workAgent.pending || m.workspace.Cursor != "101-0" {
		t.Fatal("duplicate transport woke the agent or lost replay cursor")
	}
	fresh := replayMessage(3, "102-0", 42, "new-question", "Please include the test result")
	frame, _ = json.Marshal(fresh)
	next, _ = m.workspaceFrame(workspaceFrame{generation: m.workspaceGen, frame: api.StreamFrame{Event: "workspace.event", Data: frame}})
	if !next.(model).workAgent.pending {
		t.Fatal("new peer message failed to wake the agent")
	}
}

func TestWorkspaceReplayNormalizesSnapshotDuplicates(t *testing.T) {
	m := chatModel()
	original := replayMessage(3, "100-0", 41, "requester-question", "Please verify the patch")
	duplicate := original
	duplicate.StreamID = "101-0"
	duplicate.ID = 0
	snapshot := api.Workspace{Post: api.Post{ID: 5, Status: "in_progress"}, Cursor: "101-0", State: api.WorkspaceState{LastEventID: 41}, Events: []api.WorkspaceEvent{original, duplicate}}
	next, _ := m.workspaceLoaded(workspaceLoaded{generation: m.workspaceGen, workspace: snapshot})
	m = next.(model)
	if len(m.workspace.Events) != 1 {
		t.Fatal("snapshot contains duplicate logical messages")
	}
	assertReplayTextCount(t, m, "Please verify the patch", 1)
}
