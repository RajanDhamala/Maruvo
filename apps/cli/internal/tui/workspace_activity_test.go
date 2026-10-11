package tui

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/rajandhamala/Maruvo/cli/internal/api"
	"github.com/rajandhamala/Maruvo/cli/internal/providers"
)

func activityEvent(id, actor int64, state, detail string) api.WorkspaceEvent {
	data, _ := json.Marshal(activityDetail{State: state, Detail: detail})
	return api.WorkspaceEvent{ID: id, PostID: 1, Kind: "agent.activity", ActorID: &actor, Data: data,
		CreatedAt: time.Date(2026, 10, 11, 8, 0, int(id), 0, time.UTC)}
}

func activityFixture() model {
	m := chatModel()
	m.token, m.user.ID = "fixture", "1"
	for i := int64(1); i <= 25; i++ {
		m.workspace.Events = append(m.workspace.Events, activityEvent(i, 1, "working", "… Read file · source.go"))
	}
	m.workspace.Events = append(m.workspace.Events, activityEvent(26, 1, "working", "… Run command · go test ./..."))
	m.composer.draft = textField{value: "Unsent instruction", cursor: 18, limit: 4000}
	m.composer.attachments = []localFile{{path: "/tmp/notes.txt"}}
	return m
}

func activityRowText(rows []conversationRow) string {
	var lines []string
	for _, row := range rows {
		lines = append(lines, ansi.Strip(row.text))
	}
	return strings.Join(lines, "\n")
}

func TestWorkspaceActivityIsCompactAndExpansionIsLocal(t *testing.T) {
	m := activityFixture()
	events, composer := append([]api.WorkspaceEvent(nil), m.workspace.Events...), m.composer
	rows := m.conversationRows(100)
	view := activityRowText(rows)
	if len(rows) != 5 || !strings.Contains(view, "Worked") || !strings.Contains(view, "Your agent · 26 updates") ||
		strings.Count(view, "See more") != 3 || !strings.Contains(view, "+ Show all 26 actions") || !strings.Contains(view, "source.go") {
		t.Fatal("tool activity did not show the latest three single-line actions")
	}
	next, cmd := m.toggleWorkspaceActivity(0)
	m = next.(model)
	view = activityRowText(m.conversationRows(100))
	if cmd != nil || !strings.Contains(view, "− Show fewer") || strings.Count(view, "Read file · source.go") != 25 {
		t.Fatal("expansion did not reveal the recorded tool history locally")
	}
	if !reflect.DeepEqual(m.workspace.Events, events) || !reflect.DeepEqual(m.composer, composer) || m.workspaceAction != "" || m.reviewConfirm {
		t.Fatal("activity expansion changed events, draft, attachments or payment state")
	}
	// A live update stays inside the open group, including its full reported reason.
	m.workspace.Events = append(m.workspace.Events, activityEvent(27, 1, "failed", "Command failed: regression did not pass"))
	view = activityRowText(m.conversationRows(100))
	if !strings.Contains(view, "27 updates") || !strings.Contains(view, "failed · Command failed: regression did not pass") || !strings.Contains(view, "Show fewer") {
		t.Fatal("live activity hid its failure or closed the expanded group")
	}
	// Archival identity changes do not collapse the same timestamped activity.
	m.workspace.Events[0].ID, m.workspace.Events[0].StreamID = 200, "replayed-0"
	if !strings.Contains(activityRowText(m.conversationRows(100)), "Show fewer") {
		t.Fatal("replay changed the expanded activity group")
	}
	next, cmd = m.toggleWorkspaceActivity(0)
	m = next.(model)
	if cmd != nil || len(m.conversationRows(100)) > 6 || !strings.Contains(activityRowText(m.conversationRows(100)), "failed ·") {
		t.Fatal("collapse must retain the latest failure summary")
	}
}

func TestActivityGroupsKeepMessagesFilesAndPaymentEvents(t *testing.T) {
	m := activityFixture()
	worker := int64(2)
	m.workspace.Post.UserID, m.workspace.Post.AcceptedBy = 3, &worker
	m.workspace.Files = []api.WorkspaceFile{{ID: "output", Name: "result.patch", Size: 64}}
	m.workspace.Events = []api.WorkspaceEvent{
		activityEvent(1, 1, "working", "… Read file · own.go"),
		activityEvent(2, 2, "working", "… Read file · worker.go"),
		{PostID: 1, Kind: "message", ActorID: &worker, Data: json.RawMessage(`{"text":"Patch ready"}`)},
		activityEvent(3, 2, "working", "… Write file · result.patch"),
		{PostID: 1, Kind: "file.shared", ActorID: &worker, Data: json.RawMessage(`{"id":"output","name":"result.patch","size":64}`)},
		{PostID: 1, Kind: "agent.control", ActorID: &worker, Data: json.RawMessage(`{"mode":"manual"}`)},
		{PostID: 1, Kind: "escrow.updated", Data: json.RawMessage(`{"state":"released"}`)},
		{PostID: 1, Kind: "settlement.updated", Data: json.RawMessage(`{"action":"release","state":"confirmed"}`)},
	}
	rows := m.conversationRows(100)
	view := activityRowText(rows)
	for _, term := range []string{"Your agent", "Worker agent", "Explored", "Worked", "Patch ready", "result.patch", "manual control", "Escrow: released", "Settlement: release · confirmed"} {
		if !strings.Contains(view, term) {
			t.Fatalf("compacting activity hid %s", term)
		}
	}
	if strings.Count(view, "See more") != 3 {
		t.Fatal("activity crossed an actor or message boundary")
	}
	downloads := 0
	for _, row := range rows {
		if row.fileIndex >= 0 {
			downloads++
		}
	}
	if downloads != 1 {
		t.Fatal("activity grouping lost the attached file's download target")
	}
}

func TestWorkspaceActivityControlsFitAndPreserveComposer(t *testing.T) {
	for _, size := range [][2]int{{48, 16}, {80, 24}, {120, 36}, {168, 48}} {
		m := activityFixture()
		m.width, m.height = size[0], size[1]
		composer := m.composer
		for _, expanded := range []bool{false, true} {
			found := false
			for _, hit := range m.chatLayout().hits {
				if hit.action != "workspace-activity" {
					continue
				}
				found = true
				if hit.x < 0 || hit.y < 0 || hit.x+hit.width > m.contentWidth() || hit.y+hit.height > m.bodyHeight() {
					t.Fatalf("%v: activity toggle is outside the viewport", size)
				}
				next, cmd := m.Update(tea.MouseClickMsg{Button: tea.MouseLeft, X: m.contentX() + hit.x, Y: m.bodyStart() + hit.y})
				m = next.(model)
				if cmd != nil || m.activityExpanded[activityKey(m.workspace.Events[0])] == expanded || !reflect.DeepEqual(m.composer, composer) {
					t.Fatalf("%v: activity click failed or changed the composer", size)
				}
				break
			}
			if !found {
				t.Fatalf("%v: visible activity has no expansion control", size)
			}
			if len(m.chatLayout().rows) != m.bodyHeight() {
				t.Fatalf("%v: activity pushed the composer outside the viewport", size)
			}
			for _, row := range strings.Split(m.View().Content, "\n") {
				if ansi.StringWidth(row) > size[0] {
					t.Fatalf("%v: activity exceeded terminal width", size)
				}
			}
		}
		next, cmd := m.updateComposer(tea.KeyPressMsg{Code: 'a', Text: "a", Mod: tea.ModAlt})
		m = next.(model)
		if cmd != nil || !m.activityExpanded[activityKey(m.workspace.Events[0])] || !reflect.DeepEqual(m.composer, composer) {
			t.Fatal("Alt+a must expand local activity without sending a prompt")
		}
		// Once scrolled away, the old toggle has no active mouse target.
		m.workspace.Events = append(m.workspace.Events, api.WorkspaceEvent{PostID: 1, Kind: "message",
			Data: json.RawMessage(`{"text":"` + strings.Repeat("Long message. ", 400) + `"}`)})
		for _, hit := range m.chatLayout().hits {
			if hit.action == "workspace-activity" || hit.action == "workspace-tool" {
				t.Fatal("offscreen activity still receives clicks")
			}
		}
		m.stopWorkspace()
		if m.activityExpanded != nil {
			t.Fatal("leaving the workspace retained another task's expansion state")
		}
	}
}

func TestWorkspaceToolPreviewPairsResultsAndPreservesSeparateActions(t *testing.T) {
	m := activityFixture()
	m.workspace.Events = []api.WorkspaceEvent{
		activityEvent(1, 1, "working", "… Write file · hello.asm"),
		activityEvent(2, 1, "working", "✓ Write file · hello.asm\n  Saved after approval · 1ms\nsection .data\nmessage db 'Hello', 10\nsection .text"),
		activityEvent(3, 1, "working", "… Run command · nasm hello.asm"),
		activityEvent(4, 1, "working", "× Run command · nasm hello.asm\n  Exit 1 · 4ms\nhello.asm: missing symbol"),
	}
	events := append([]api.WorkspaceEvent(nil), m.workspace.Events...)
	view := activityRowText(m.conversationRows(100))
	if strings.Count(view, "Write file") != 1 || strings.Count(view, "Run command") != 1 ||
		!strings.Contains(view, "hello.asm") || !strings.Contains(view, "Exit 1") || strings.Contains(view, "section .data") {
		t.Fatal("start and result were duplicated, or the code preview was not collapsed")
	}
	next, cmd := m.toggleWorkspaceTool(0)
	m = next.(model)
	view = activityRowText(m.conversationRows(100))
	if cmd != nil || !strings.Contains(view, "section .data") || strings.Contains(view, "missing symbol") ||
		strings.Count(view, "See less") != 1 || !reflect.DeepEqual(m.workspace.Events, events) {
		t.Fatal("expanding a file changed events or opened another action's output")
	}
	next, cmd = m.toggleWorkspaceTool(2)
	m = next.(model)
	if cmd != nil || !strings.Contains(activityRowText(m.conversationRows(100)), "hello.asm: missing symbol") {
		t.Fatal("failed command did not expose its captured output")
	}
	m.workspace.Events = append(m.workspace.Events,
		activityEvent(5, 1, "working", "… Write file · hello.asm"),
		activityEvent(6, 1, "working", "✓ Write file · hello.asm\n  Saved after approval · 1ms\nnew version"))
	view = activityRowText(m.conversationRows(100))
	if strings.Count(view, "Write file") != 2 || strings.Contains(view, "new version") {
		t.Fatal("repeated writes were merged, or the old preview expansion opened the new version")
	}
}

func TestWorkspaceToolPreviewSurvivesCompletionAndShowsLegacyLimit(t *testing.T) {
	m := activityFixture()
	m.workspace.Events = []api.WorkspaceEvent{activityEvent(1, 1, "working", "… Read file · main.go")}
	next, cmd := m.updateComposer(tea.KeyPressMsg{Code: 'a', Text: "a", Mod: tea.ModAlt})
	m = next.(model)
	if cmd != nil || !strings.Contains(activityRowText(m.conversationRows(80)), "Running; the result appears") {
		t.Fatal("an unfinished tool fabricated a file preview")
	}
	m.workspace.Events = append(m.workspace.Events, activityEvent(2, 1, "working", "✓ Read file · main.go\n  1 line · 13 bytes · 1ms\npackage main"))
	view := activityRowText(m.conversationRows(80))
	if !strings.Contains(view, "package main") || strings.Count(view, "Read file") != 1 {
		t.Fatal("completion did not replace the running row and retain its open preview")
	}
	m.workspace.Events = []api.WorkspaceEvent{activityEvent(1, 1, "working", "Read file")}
	if !strings.Contains(activityRowText(m.conversationRows(80)), "Preview not recorded") {
		t.Fatal("legacy generic activity invented file content")
	}
}

func TestWorkspaceToolPreviewClicksStayWithinViewport(t *testing.T) {
	for _, size := range [][2]int{{48, 16}, {80, 24}, {120, 36}, {168, 48}} {
		m := activityFixture()
		m.width, m.height = size[0], size[1]
		m.workspace.Events = []api.WorkspaceEvent{activityEvent(1, 1, "working", "✓ Write file · hello.asm\n  Saved\nsection .text\nglobal _start")}
		composer := m.composer
		for _, expanded := range []bool{false, true} {
			found := false
			for _, hit := range m.chatLayout().hits {
				if hit.action != "workspace-tool" {
					continue
				}
				found = true
				if hit.x < 0 || hit.y < 0 || hit.x+hit.width > m.contentWidth() || hit.y+hit.height > m.bodyHeight() {
					t.Fatalf("%v: tool preview control is outside the viewport", size)
				}
				next, cmd := m.Update(tea.MouseClickMsg{Button: tea.MouseLeft, X: m.contentX() + hit.x, Y: m.bodyStart() + hit.y})
				m = next.(model)
				if cmd != nil || m.activityExpanded["tool/"+activityKey(m.workspace.Events[0])] == expanded || !reflect.DeepEqual(m.composer, composer) {
					t.Fatalf("%v: preview click changed the draft or invoked work", size)
				}
				break
			}
			if !found {
				t.Fatalf("%v: preview cannot be collapsed or expanded", size)
			}
			for _, row := range strings.Split(m.View().Content, "\n") {
				if ansi.StringWidth(row) > size[0] {
					t.Fatalf("%v: preview exceeds terminal width", size)
				}
			}
		}
	}
}

func TestToolActivityDetailFitsExistingAPIContract(t *testing.T) {
	detail := toolActivityDetail(providers.Event{Type: "tool", Text: "✓ Read file · source.go\n  5 lines", Preview: strings.Repeat("界", 2000) + "\n\x1b[31mend\x00"})
	if utf8.RuneCountInString(detail) > 1000 || len(detail) > 4000 || !utf8.ValidString(detail) ||
		strings.Contains(detail, "\x1b") || strings.Contains(detail, "\x00") || !strings.HasPrefix(detail, "✓ Read file · source.go") || !strings.HasSuffix(detail, "\n…") {
		t.Fatal("preview escaped the existing activity size or terminal-text contract")
	}
}
