package tui

import (
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/rajandhamala/Maruvo/cli/internal/api"
)

func TestSpaceEndsCommandSearchAndPreservesInstruction(t *testing.T) {
	for _, query := range []string{"/agent ", "/agent start solving problem", "/agent\tstart solving problem"} {
		m := model{screen: workspaceScreen, commands: commandMenu{query: textField{value: query}}}
		indices := m.commandIndices()
		if len(indices) != 1 || slashCommands[indices[0]].name != "/agent" {
			t.Fatalf("arguments still searched as command: %q", query)
		}
	}
	name, argument, complete := commandParts("/agent start solving  problem")
	if name != "/agent" || argument != "start solving  problem" || !complete {
		t.Fatal("instruction changed")
	}
}

func TestAgentInstructionStartsCurrentTaskRunner(t *testing.T) {
	worker := int64(1)
	m := model{screen: workspaceScreen, user: api.User{ID: "1"}, directory: t.TempDir(), workspace: api.Workspace{Post: api.Post{ID: 5, UserID: 3, AcceptedBy: &worker, Status: "in_progress"}}, commands: commandMenu{open: true, query: textField{value: "/agent start solving problem"}}}
	next, cmd := m.updateCommands(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = next.(model)
	if cmd == nil || m.commands.open || !m.workAgent.active || m.workSetup.open || m.workAgent.postID != 5 || m.workAgent.instruction != "start solving problem" {
		t.Fatal("typed command did not start task agent with instruction")
	}
}

func TestWorkArgumentsQueueWithoutRestartingActiveRunner(t *testing.T) {
	worker := int64(1)
	m := model{screen: workspaceScreen, user: api.User{ID: "1"}, workspace: api.Workspace{Post: api.Post{ID: 5, UserID: 3, AcceptedBy: &worker, Status: "in_progress"}}, workAgent: workspaceAgent{active: true, postID: 5, generation: 9}, commands: commandMenu{open: true, query: textField{value: "/work check the reconnect handler"}}}
	next, _ := m.updateCommands(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = next.(model)
	if m.workAgent.generation != 9 || !m.workAgent.pending || m.workAgent.instruction != "check the reconnect handler" || m.commands.open {
		t.Fatal("instruction restarted or missed active runner")
	}
}

func TestVisibleAgentStartUsesDraftAndPreservesAttachments(t *testing.T) {
	worker := int64(1)
	m := model{screen: workspaceScreen, user: api.User{ID: "1"}, directory: t.TempDir(), workspace: api.Workspace{Post: api.Post{ID: 5, UserID: 3, AcceptedBy: &worker, Status: "in_progress"}}, composer: chatComposer{draft: textField{value: "start solving problem"}, attachments: []localFile{{path: "patch.diff"}}}}
	next, cmd := m.workspaceCommand("workspace-agent")
	m = next.(model)
	if cmd == nil || m.workAgent.active || !m.workSetup.open || m.workSetup.instruction != "start solving problem" || m.composer.draft.value != "" || len(m.composer.attachments) != 1 {
		t.Fatal("visible start lost task instruction or attachments")
	}
	next, _ = m.workspaceCommand("workspace-agent")
	m = next.(model)
	if m.workAgent.active {
		t.Fatal("visible stop did not stop agent")
	}
}

func TestRequesterCanInstructOwnAgentWhileInvitationPending(t *testing.T) {
	worker := int64(1)
	m := model{screen: workspaceScreen, user: api.User{ID: "3"}, directory: t.TempDir(), workspace: api.Workspace{Post: api.Post{ID: 11, UserID: 3, AcceptedBy: &worker, Status: "in_progress"}}, workSetup: workSetup{armed: true, postID: 11}, commands: commandMenu{open: true, query: textField{value: "/agent download the delivery to /tmp"}}}
	next, cmd := m.updateCommands(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = next.(model)
	if cmd == nil || !m.workAgent.active || m.workAgent.instruction != "download the delivery to /tmp" || m.workSetup.open || !m.workSetup.armed {
		t.Fatal("requester's local instruction was gated on the peer invitation")
	}
}

func TestPastedAgentCommandStaysLocalInWorkspace(t *testing.T) {
	worker := int64(1)
	m := model{screen: workspaceScreen, user: api.User{ID: "3"}, directory: t.TempDir(), workspace: api.Workspace{Post: api.Post{ID: 11, UserID: 3, AcceptedBy: &worker, Status: "in_progress"}}, composer: chatComposer{draft: textField{value: "/agent download files into /tmp"}}}
	next, cmd := m.sendChat()
	m = next.(model)
	if cmd == nil || !m.workAgent.active || m.workAgent.instruction != "download files into /tmp" || m.localAgentVisible() || m.composer.draft.value != "" {
		t.Fatal("pasted agent instruction did not stay local in the workspace")
	}
}
