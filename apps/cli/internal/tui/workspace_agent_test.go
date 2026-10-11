package tui

import (
	"context"
	"reflect"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/rajandhamala/Maruvo/cli/internal/api"
	"github.com/rajandhamala/Maruvo/cli/internal/providers"
)

func TestPausedTaskAgentCanContinueInWorkspace(t *testing.T) {
	for _, input := range []string{"click", "slash command", "pasted command"} {
		t.Run(input, func(t *testing.T) {
			m := streamingFixture(t)
			worker := int64(7)
			m.screen = workspaceScreen
			m.token = "fixture"
			m.workspace.Post = api.Post{ID: 13, UserID: 3, AcceptedBy: &worker, Status: "in_progress"}
			m.posts = []api.Post{m.workspace.Post}
			m.workAgent = workspaceAgent{active: true, postID: 13, generation: 8, turns: 1, started: time.Now()}
			m.localAgent.marketplace = &providers.Marketplace{TaskID: 13}
			m.client = api.NewClient("http://127.0.0.1:1")
			m.composer.draft = textField{value: "Keep my draft", limit: 4000}
			m.composer.attachments = []localFile{{path: "notes.txt"}}
			history := []providers.Message{{Role: "tool", Content: "build completed", ToolCallID: "build-1"}}
			next, _ := m.Update(localAgentUpdate{sequence: 4, done: true, err: providers.ErrToolBatchPaused, history: history})
			m = next.(model)
			if !m.workAgent.paused || m.localAgentVisible() {
				t.Fatal("fixture must pause while the transcript is hidden")
			}
			if !strings.Contains(ansi.Strip(m.View().Content), "Continue agent") {
				t.Fatal("paused task has no visible Continue control")
			}
			ctx, cancel := context.WithCancel(m.ctx)
			cancel()
			m.ctx = ctx
			var cmd tea.Cmd
			switch input {
			case "click":
				found := false
				for _, hit := range m.postsLayout().hits {
					if hit.action == "workspace-continue" {
						next, cmd = m.Update(tea.MouseClickMsg{Button: tea.MouseLeft, X: m.contentX() + hit.x, Y: m.bodyStart() + hit.y})
						found = true
						break
					}
				}
				if !found {
					t.Fatal("Continue control has no click target")
				}
			case "slash command":
				m.commands = commandMenu{open: true, query: textField{value: "/continue"}}
				next, cmd = m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
			case "pasted command":
				m.composer.attachments = nil
				m.composer.draft = textField{value: "/continue", limit: 4000}
				next, cmd = m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
			}
			m = next.(model)
			if cmd == nil || m.workAgent.paused || !m.workAgent.active || !m.localAgent.busy || m.commands.open ||
				m.workAgent.turns != 2 || m.workAgent.generation != 8 || m.localAgent.marketplace.TaskID != 13 ||
				!reflect.DeepEqual(m.localAgent.history, history) {
				t.Fatal("Continue lost task state, tool history or attachment, or failed to resume")
			}
			if input != "pasted command" && m.composer.draft.value != "Keep my draft" {
				t.Fatal("Continue button or menu lost the workspace draft")
			}
			if input != "pasted command" && len(m.composer.attachments) != 1 {
				t.Fatal("Continue button or menu lost the workspace attachment")
			}
			if input == "pasted command" && m.composer.draft.value != "" {
				t.Fatal("local Continue command remained in chat")
			}
			m.localAgent.cancel()
		})
	}
}

func TestWorkspaceLoopWakesOnlyForNewRelevantWork(t *testing.T) {
	own, other := int64(1), int64(3)
	m := model{user: api.User{ID: "1"}, workspace: api.Workspace{Post: api.Post{ID: 4, Status: "in_progress"}}, workAgent: workspaceAgent{active: true, postID: 4}}
	for _, event := range []api.WorkspaceEvent{
		{PostID: 8, Kind: "message", ActorID: &other},
		{PostID: 4, Kind: "agent.activity", ActorID: &other},
		{PostID: 4, Kind: "message", ActorID: &own, Data: []byte(`{"agent_name":"built-in agent"}`)},
		{PostID: 4, Kind: "file.shared", ActorID: &own},
	} {
		m.wakeWorkAgent(event)
		if m.workAgent.pending {
			t.Fatalf("feedback loop for %s", event.Kind)
		}
	}
	for _, kind := range []string{"message", "file.shared", "escrow.updated", "work.submitted", "review.changes_requested"} {
		m.workAgent.pending = false
		m.wakeWorkAgent(api.WorkspaceEvent{PostID: 4, Kind: kind, ActorID: &other})
		if !m.workAgent.pending {
			t.Fatalf("lost wakeup %s", kind)
		}
	}
	m.workspace.AgentControl.Mode = "manual"
	m.wakeWorkAgent(api.WorkspaceEvent{PostID: 4, Kind: "agent.control"})
	if m.workAgent.active {
		t.Fatal("manual takeover did not stop loop")
	}
}

func TestWorkspaceLoopLimitsAndStopCancelInFlightWork(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	m := model{screen: workspaceScreen, workspace: api.Workspace{Post: api.Post{ID: 4}}, workAgent: workspaceAgent{active: true, pending: true, postID: 4, generation: 2, turns: 20, started: time.Now()}, localAgent: localAgentState{cancel: cancel, marketplace: &providers.Marketplace{TaskID: 4}}}
	bound := m.localAgent.marketplace
	next, _ := m.advanceWorkAgent(workspaceAgentTick{generation: 2})
	m = next.(model)
	if m.workAgent.active || ctx.Err() == nil || m.localAgent.marketplace.TaskID != 0 || bound.TaskID != 4 {
		t.Fatal("loop limit failed or mutated in-flight scope")
	}
}

func TestWorkspaceLoopIdleDoesNotStartModel(t *testing.T) {
	m := model{screen: workspaceScreen, workspace: api.Workspace{Post: api.Post{ID: 4}}, workAgent: workspaceAgent{active: true, postID: 4, generation: 2, started: time.Now()}}
	m.setPermissionMode(2)
	next, cmd := m.advanceWorkAgent(workspaceAgentTick{generation: 2})
	m = next.(model)
	if cmd == nil || m.workAgent.turns != 0 || m.localAgent.busy {
		t.Fatal("idle loop started model")
	}
	next, cmd = m.advanceWorkAgent(workspaceAgentTick{generation: 1})
	if cmd != nil || next.(model).workAgent.turns != 0 {
		t.Fatal("stale loop generation resumed")
	}
}

func pausedTaskAgentFixture(t *testing.T) model {
	t.Helper()
	m := streamingFixture(t)
	worker := int64(7)
	m.screen, m.token = workspaceScreen, "fixture"
	m.workspace.Post = api.Post{ID: 13, UserID: 3, AcceptedBy: &worker, Status: "in_progress"}
	m.workAgent = workspaceAgent{active: true, postID: 13, generation: 8, turns: 1, started: time.Now()}
	m.localAgent.marketplace = &providers.Marketplace{TaskID: 13}
	m.client = api.NewClient("http://127.0.0.1:1")
	m.composer.draft = textField{value: "Keep my draft", limit: 4000}
	m.composer.attachments = []localFile{{path: "notes.txt"}}
	next, _ := m.Update(localAgentUpdate{sequence: 4, done: true, err: providers.ErrToolBatchPaused,
		history: []providers.Message{{Role: "tool", Content: "build completed", ToolCallID: "build-1"}}})
	m = next.(model)
	ctx, cancel := context.WithCancel(m.ctx)
	cancel()
	m.ctx = ctx // No provider or activity requests are made by this fixture.
	return m
}

func TestAllowedTaskAgentAutomaticallyResumesExactBatch(t *testing.T) {
	m := pausedTaskAgentFixture(t)
	m.setPermissionMode(2)
	history := m.localAgent.history
	next, cmd := m.advanceWorkAgent(workspaceAgentTick{generation: 8})
	m = next.(model)
	t.Cleanup(m.localAgent.cancel)
	if cmd == nil || m.workAgent.paused || !m.localAgent.busy || !m.workAgent.active ||
		m.workAgent.turns != 2 || m.workAgent.generation != 8 || m.localAgent.marketplace.TaskID != 13 ||
		!reflect.DeepEqual(m.localAgent.history, history) || m.composer.draft.value != "Keep my draft" || len(m.composer.attachments) != 1 {
		t.Fatal("automatic continuation lost completed tool history, task scope or workspace input")
	}
	next, _ = m.advanceWorkAgent(workspaceAgentTick{generation: 8})
	if next.(model).workAgent.turns != 2 {
		t.Fatal("another tick started a duplicate model batch")
	}
}

func TestTaskAgentWaitsWithoutSessionConsentOrDuringPermissionMenu(t *testing.T) {
	for _, mode := range []int{0, 1, 3} {
		m := pausedTaskAgentFixture(t)
		m.setPermissionMode(mode)
		next, _ := m.advanceWorkAgent(workspaceAgentTick{generation: 8})
		m = next.(model)
		if !m.workAgent.active || !m.workAgent.paused || m.localAgent.busy || m.workAgent.turns != 1 {
			t.Fatalf("mode %d resumed without explicit session consent", mode)
		}
	}
	m := pausedTaskAgentFixture(t)
	next, _ := m.openPermissions()
	m = next.(model)
	if !m.permissions.open || !m.workAgent.active || !m.workAgent.paused || len(m.localAgent.history) != 1 {
		t.Fatal("changing permissions discarded paused work")
	}
	m.setPermissionMode(2)
	m.permissions.open = true
	next, _ = m.advanceWorkAgent(workspaceAgentTick{generation: 8})
	m = next.(model)
	if m.localAgent.busy || m.workAgent.turns != 1 {
		t.Fatal("automatic continuation interrupted the permission menu")
	}
}

func TestAutomaticContinuationRespectsTaskAndRunBounds(t *testing.T) {
	for _, test := range []struct {
		name   string
		change func(*model)
	}{
		{"turn limit", func(m *model) { m.workAgent.turns = 20 }},
		{"time limit", func(m *model) { m.workAgent.started = time.Now().Add(-31 * time.Minute) }},
		{"completed", func(m *model) { m.workspace.Post.Status = "completed" }},
		{"cancelled", func(m *model) { m.workspace.Post.Status = "cancelled" }},
		{"takeover", func(m *model) { m.workspace.AgentControl.Mode = "manual" }},
		{"different task", func(m *model) { m.localAgent.marketplace.TaskID = 99 }},
		{"stopped", func(m *model) { m.stopWorkAgent("Stopped") }},
	} {
		t.Run(test.name, func(t *testing.T) {
			m := pausedTaskAgentFixture(t)
			m.setPermissionMode(2)
			test.change(&m)
			turns := m.workAgent.turns
			next, _ := m.advanceWorkAgent(workspaceAgentTick{generation: 8})
			m = next.(model)
			if m.localAgent.busy || m.workAgent.turns != turns {
				t.Fatal("automatic continuation bypassed task authority or execution bounds")
			}
		})
	}
}

func TestLastAllowedTaskBatchFinishesBeforeTurnLimit(t *testing.T) {
	m := pausedTaskAgentFixture(t)
	m.setPermissionMode(2)
	m.workAgent.turns = 19
	next, _ := m.advanceWorkAgent(workspaceAgentTick{generation: 8})
	m = next.(model)
	t.Cleanup(m.localAgent.cancel)
	next, _ = m.advanceWorkAgent(workspaceAgentTick{generation: 8})
	m = next.(model)
	if m.workAgent.turns != 20 || !m.workAgent.active || !m.localAgent.busy {
		t.Fatal("turn limit interrupted the twentieth batch")
	}
	next, _ = m.Update(localAgentUpdate{sequence: m.localAgent.sequence, done: true,
		err: providers.ErrToolBatchPaused, history: m.localAgent.history})
	m = next.(model)
	next, _ = m.advanceWorkAgent(workspaceAgentTick{generation: 8})
	if next.(model).workAgent.active || next.(model).workAgent.turns != 20 {
		t.Fatal("turn limit allowed another batch")
	}
}
