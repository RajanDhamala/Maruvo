package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/rajandhamala/Maruvo/cli/internal/api"
	"github.com/rajandhamala/Maruvo/cli/internal/providers"
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

func workspaceAgentControlsFixture(t *testing.T, requester, connected bool) model {
	t.Helper()
	m := chatModel()
	worker := int64(1)
	m.ctx, m.workspaceCtx, m.token = t.Context(), t.Context(), "fixture"
	m.user.ID = "1"
	if requester {
		m.user.ID = "3"
	}
	m.workspace.Post.UserID, m.workspace.Post.AcceptedBy = 3, &worker
	m.posts = []api.Post{m.workspace.Post}
	if connected {
		m.agentConfig = providers.Config{Active: "deepseek", Connections: map[string]providers.Connection{
			"deepseek": {Model: "deepseek-flash", Storage: "encrypted"},
		}}
	}
	m.composer.draft.value = "Check the task and report progress"
	m.composer.attachments = []localFile{{path: "/tmp/notes.txt"}}
	return m
}

func TestWorkspaceAgentButtonsSeparateLocalWorkAndInvitations(t *testing.T) {
	for _, requester := range []bool{false, true} {
		for _, size := range [][2]int{{48, 16}, {80, 24}, {120, 36}, {168, 48}} {
			for _, action := range []string{"workspace-agent", "workspace-invite"} {
				m := workspaceAgentControlsFixture(t, requester, true)
				m.width, m.height = size[0], size[1]
				view := ansi.Strip(m.View().Content)
				for _, label := range []string{"Start my agent", "Invite both agents"} {
					if !strings.Contains(view, label) {
						t.Fatalf("missing %s at %v requester=%v", label, size, requester)
					}
				}
				for _, row := range strings.Split(view, "\n") {
					if ansi.StringWidth(row) > size[0] {
						t.Fatalf("agent buttons overflow at %v", size)
					}
				}
				found := false
				for _, hit := range m.chatLayout().hits {
					if hit.action != action {
						continue
					}
					found = true
					next, cmd := m.Update(tea.MouseClickMsg{Button: tea.MouseLeft, X: m.contentX() + hit.x + 1, Y: m.bodyStart() + hit.y})
					m = next.(model)
					if cmd == nil || len(m.composer.attachments) != 1 || m.composer.draft.value != "" || m.screen != workspaceScreen {
						t.Fatal("agent control lost the workspace or failed to apply its instruction")
					}
					if action == "workspace-agent" {
						if !m.workAgent.active || m.workSetup.open || m.workSetup.armed || m.workSetup.invite || m.workAgent.instruction != "Check the task and report progress" {
							t.Fatal("local start sent an invitation or waited for the other participant")
						}
					} else if m.workAgent.active || !m.workSetup.open || !m.workSetup.invite || m.workSetup.armed {
						t.Fatal("invitation started an agent before setup and peer acceptance")
					}
					break
				}
				if !found {
					t.Fatalf("missing clickable %s at %v", action, size)
				}
			}
		}
	}
}

func TestWorkspaceWithoutAgentOffersConnectionAndCannotStartOrInvite(t *testing.T) {
	for _, action := range []string{"workspace-agent", "workspace-invite", "workspace-connect-agent"} {
		m := workspaceAgentControlsFixture(t, false, false)
		view := ansi.Strip(m.View().Content)
		if !strings.Contains(view, "Connect agent") || strings.Contains(view, "Start my agent") || strings.Contains(view, "Invite both agents") {
			t.Fatal("account without an agent exposed work controls")
		}
		next, _ := m.workspaceCommand(action)
		m = next.(model)
		if !m.providers.open || m.workAgent.active || m.workSetup.open || m.workSetup.invite || m.composer.draft.value != "Check the task and report progress" {
			t.Fatal("missing-agent control started work, invited a peer or discarded the draft")
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
