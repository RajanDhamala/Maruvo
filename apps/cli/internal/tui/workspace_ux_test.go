package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/rajandhamala/Maruvo/cli/internal/api"
)

func TestWorkspaceCommandsPreserveAttachmentsAndReturnToPrompt(t *testing.T) {
	m := chatModel()
	m.token = "session"
	m.composer.attachments = []localFile{{path: "/tmp/patch.diff"}}
	next, _ := m.Update(tea.KeyPressMsg{Code: '/', Text: "/"})
	m = next.(model)
	if !m.commands.open || !strings.Contains(ansi.Strip(m.View().Content), "/connect") {
		t.Fatal("workspace must show shared commands")
	}
	m.commands.query = textField{value: "/connect", cursor: 8, limit: 100}
	next, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = next.(model)
	if !m.providers.open || m.commands.open || len(m.composer.attachments) != 1 || m.screen != workspaceScreen {
		t.Fatal("provider settings must preserve the workspace")
	}
	m.providers.open = false
	m = m.openCommands()
	m.commands.query = textField{value: "@patch", cursor: 6, limit: 100}
	next, _ = m.updateCommandQuery()
	m = next.(model)
	if m.commands.open || !m.composer.completing || m.composer.draft.value != "@patch" || m.homeInput.value != "" {
		t.Fatal("typing ordinary text must return to workspace completion")
	}
	for _, name := range []string{"/model", "/connect", "/new", "/attach", "/files", "/review"} {
		m = chatModel()
		m.commands.query.value = name
		indices := m.commandIndices()
		if len(indices) == 0 {
			t.Fatalf("missing %s", name)
		}
		next, _ = m.chooseCommand(indices[0])
		m = next.(model)
		if name == "/new" && (!m.localAgent.open || m.localAgent.input.value != "") {
			t.Fatal("new must start an agent conversation")
		}
	}
}

func TestWorkspacePresenceIsSeparateFromAgentLease(t *testing.T) {
	worker := int64(2)
	m := chatModel()
	m.token = "session"
	m.user.ID, m.live = "2", "Live"
	m.workspace.Post.UserID, m.workspace.Post.AcceptedBy = 1, &worker
	m.workspace.Post.Remote = api.RemoteStatus{Status: "waiting_for_funding", WorkerOnline: false}
	m.presence = &api.WorkspacePresence{RequesterOnline: true}
	view := ansi.Strip(m.View().Content)
	if !strings.Contains(view, "Requester · online in workspace") || !strings.Contains(view, "Connected") || strings.Contains(view, "seller offline") {
		t.Fatal("human presence must not come from the seller agent lease")
	}
	m.presence.RequesterOnline = false
	if !strings.Contains(ansi.Strip(m.View().Content), "away from workspace") {
		t.Fatal("missing away status")
	}
	m.live = "Reconnecting..."
	if !strings.Contains(m.workspacePeerLabel(), "unavailable") {
		t.Fatal("disconnected client must not claim fresh presence")
	}
	m.live, m.user.ID = "Live", "1"
	m.presence.WorkerOnline = true
	if m.workspacePeerLabel() != "Worker · online in workspace" {
		t.Fatal("poster must see worker presence")
	}
}

func TestWorkspacePromptCardFitsWithDraftAndAttachments(t *testing.T) {
	for _, size := range [][2]int{{48, 16}, {80, 24}, {120, 36}, {180, 48}} {
		for _, attachments := range []bool{false, true} {
			m := chatModel()
			m.token = "session"
			m.width, m.height = size[0], size[1]
			m.composer.draft = textField{value: "First line\nSecond line\nThird line", cursor: 12, limit: 4000}
			if attachments {
				m.composer.attachments = []localFile{{path: "/tmp/patch.diff"}}
			}
			layout := m.chatLayout()
			if len(layout.rows) > m.bodyHeight() {
				t.Fatalf("%v attachments=%v: composer clipped", size, attachments)
			}
			view := m.View()
			content := ansi.Strip(view.Content)
			if !strings.Contains(content, "Enter send") || !strings.Contains(content, "Task chat") || view.BackgroundColor == nil {
				t.Fatalf("%v: shared prompt style/action missing", size)
			}
			for _, row := range strings.Split(view.Content, "\n") {
				if ansi.StringWidth(row) > size[0] {
					t.Fatalf("%v: row overflow", size)
				}
			}
		}
	}
}

func TestPresenceRefreshPreservesDraftAndRejectsPreviousWorkspace(t *testing.T) {
	m := chatModel()
	m.composer.draft.value = "Unsent message"
	for _, generation := range []uint64{m.workspaceGen - 1, m.workspaceGen} {
		next, _ := m.Update(remoteStatusLoaded{generation: generation, presence: &api.WorkspacePresence{RequesterOnline: true}})
		m = next.(model)
		if (m.presence != nil) != (generation == m.workspaceGen) || m.composer.draft.value != "Unsent message" {
			t.Fatal("presence update changed a draft or leaked between tasks")
		}
	}
}
