package tui

import (
	"strings"
	"sync"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/rajandhamala/Maruvo/cli/internal/api"
	"github.com/rajandhamala/Maruvo/cli/internal/providers"
)

func TestActionApprovalMenuConfirmsOnlySelectedAction(t *testing.T) {
	for _, test := range []struct {
		name string
		keys []rune
		yes  bool
	}{
		{"default no", []rune{tea.KeyEnter}, false},
		{"select yes", []rune{tea.KeyUp, tea.KeyEnter}, true},
		{"yes shortcut", []rune{'y'}, true},
		{"escape declines", []rune{tea.KeyEscape}, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			answer := make(chan bool, 1)
			m := model{localAgent: localAgentState{open: true, busy: true, approvalChoice: approvalNo, approval: &providers.Approval{Action: "Run command", Path: "/tmp/project", Content: "go test ./..."}, answer: answer}}
			for _, key := range test.keys {
				next, _ := m.updateLocalAgent(tea.KeyPressMsg{Code: key})
				m = next.(model)
			}
			select {
			case yes := <-answer:
				if yes != test.yes {
					t.Fatal("wrong action decision")
				}
			default:
				t.Fatal("decision missing")
			}
			if m.localAgent.approval != nil || !m.localAgent.busy {
				t.Fatal("approval did not resume existing agent turn")
			}
		})
	}
}

func TestWorkspaceApprovalOwnsKeyboardInput(t *testing.T) {
	for _, test := range []struct {
		name string
		keys []rune
		yes  bool
	}{
		{"default decline", []rune{tea.KeyEnter}, false},
		{"select allow", []rune{tea.KeyUp, tea.KeyEnter}, true},
		{"escape declines", []rune{tea.KeyEscape}, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			m, answer := workspaceApprovalFixture(t)
			for _, key := range test.keys {
				next, _ := m.Update(tea.KeyPressMsg{Code: key})
				m = next.(model)
			}
			select {
			case yes := <-answer:
				if yes != test.yes {
					t.Fatal("wrong approval decision")
				}
			default:
				t.Fatal("workspace swallowed approval keys")
			}
			if m.localAgent.approval != nil || !m.localAgent.busy || !m.workAgent.active ||
				m.screen != workspaceScreen || m.activityScroll != 0 || m.composer.draft.value != "Keep draft" {
				t.Fatal("approval keys changed the workspace or stopped its agent")
			}
		})
	}
}

func TestWorkspaceApprovalClickConfirmsVisibleChoice(t *testing.T) {
	for _, size := range [][2]int{{48, 16}, {80, 24}, {120, 36}} {
		for _, test := range []struct {
			label string
			yes   bool
		}{{"Accept once", true}, {"› No", false}} {
			m, answer := workspaceApprovalFixture(t)
			m.width, m.height = size[0], size[1]
			before := m.localAgent.approval
			next, _ := m.Update(tea.MouseClickMsg{Button: tea.MouseLeft, X: 2, Y: 1})
			m = next.(model)
			if m.localAgent.approval != before || m.profileOpen {
				t.Fatal("background click escaped the approval panel")
			}
			row := -1
			for y, line := range strings.Split(ansi.Strip(m.View().Content), "\n") {
				if strings.TrimSpace(line) == test.label {
					row = y
					break
				}
			}
			if row < 0 {
				t.Fatalf("missing visible choice %q at %v", test.label, size)
			}
			next, _ = m.Update(tea.MouseClickMsg{Button: tea.MouseLeft, X: 5, Y: row})
			m = next.(model)
			select {
			case yes := <-answer:
				if yes != test.yes {
					t.Fatal("click confirmed the wrong choice")
				}
			default:
				t.Fatalf("visible approval choice did not respond at %v", size)
			}
			if m.localAgent.approval != nil || !m.localAgent.busy || !m.workAgent.active {
				t.Fatal("click did not resume the existing agent turn")
			}
		}
	}
}

func workspaceApprovalFixture(t *testing.T) (model, chan bool) {
	t.Helper()
	worker := int64(1)
	answer := make(chan bool, 1)
	m := model{
		ctx: t.Context(), width: 120, height: 36, screen: workspaceScreen, token: "fixture",
		user: api.User{ID: "1"}, workspace: api.Workspace{Post: api.Post{ID: 13, UserID: 3, AcceptedBy: &worker}},
		workAgent: workspaceAgent{active: true, postID: 13},
		localAgent: localAgentState{
			open: true, busy: true, approvalChoice: approvalNo,
			approval: &providers.Approval{Action: "Run command", Path: "/tmp/project", Content: "uname -m"},
			answer:   answer, approvalInput: textField{limit: 2000, byteLimit: 8000},
		},
	}
	m.posts = []api.Post{m.workspace.Post}
	m.composer.draft = textField{value: "Keep draft"}
	return m, answer
}

func TestWorkspaceApprovalGuidanceAndPermissionsRemainInteractive(t *testing.T) {
	m, answer := workspaceApprovalFixture(t)
	guidance := ""
	m.localAgent.approvalGuidance = &guidance
	for _, msg := range []tea.Msg{
		tea.KeyPressMsg{Code: 'g', Text: "g"},
		tea.PasteMsg{Content: "Use the existing assembler"}, tea.KeyPressMsg{Code: tea.KeyEnter},
	} {
		next, _ := m.Update(msg)
		m = next.(model)
	}
	select {
	case yes := <-answer:
		if yes || guidance != "Use the existing assembler" {
			t.Fatal("guidance did not decline the action and reach the model")
		}
	default:
		t.Fatal("workspace swallowed approval guidance")
	}

	m, answer = workspaceApprovalFixture(t)
	for _, key := range []rune{'a'} {
		next, _ := m.Update(tea.KeyPressMsg{Code: key})
		m = next.(model)
	}
	if !m.permissions.open || m.localAgent.approval == nil {
		t.Fatal("Change permissions failed to open over the pending approval")
	}
	for _, key := range []rune{tea.KeyDown, tea.KeyEnter} {
		next, _ := m.Update(tea.KeyPressMsg{Code: key})
		m = next.(model)
	}
	if m.permissions.open || m.permissionMode() != 1 || m.localAgent.approval == nil || !m.workAgent.active {
		t.Fatal("permission menu failed to return to the pending approval")
	}
	select {
	case <-answer:
		t.Fatal("changing permissions answered the pending command approval")
	default:
	}
}

func TestWorkspaceApprovalWheelScrollsDetailsWithoutMovingWorkspace(t *testing.T) {
	m, answer := workspaceApprovalFixture(t)
	m.localAgent.approval.Content = strings.Repeat("command details\n", 20)
	next, _ := m.Update(tea.MouseWheelMsg{Button: tea.MouseWheelDown})
	m = next.(model)
	if m.localAgent.approvalScroll == 0 || m.activityScroll != 0 || m.localAgent.approvalChoice != approvalNo {
		t.Fatal("approval detail scrolling changed the workspace or selection")
	}
	select {
	case <-answer:
		t.Fatal("scrolling answered the approval")
	default:
	}
}

func TestActionApprovalShowsChoicesAndExactCommand(t *testing.T) {
	m := model{width: 80, height: 24, localAgent: localAgentState{open: true, approvalChoice: approvalNo, approval: &providers.Approval{Action: "Run command", Path: "/tmp/project", Content: "go test ./..."}}}
	view := ansi.Strip(m.actionApprovalView().Content)
	for _, text := range []string{"Allow this action?", "Accept", "No", "Allow all for this session", "go test ./...", "/tmp/project"} {
		if !strings.Contains(view, text) {
			t.Fatalf("missing %s in %s", text, view)
		}
	}
}

func TestAllowAllCoversLocalActionsAndIsScoped(t *testing.T) {
	m, answer := workspaceApprovalFixture(t)
	m.profile, m.directory = "default", t.TempDir()
	m.permissions.remembered = &sync.Map{}
	captured := m
	for _, key := range []rune{tea.KeyDown, tea.KeyEnter} {
		next, _ := m.Update(tea.KeyPressMsg{Code: key})
		m = next.(model)
	}
	if !<-answer || !m.hasFullAccess() || m.permissions.open || m.permissions.confirm {
		t.Fatal("Allow all must approve the pending action in one decision")
	}
	for _, action := range []string{"", "Run command", "Share with remote task", "Download from remote task"} {
		if !captured.permitsAction(action) {
			t.Fatalf("running callback would ask again for %q", action)
		}
	}
	for _, change := range []func(*model){
		func(m *model) { m.user.ID = "3" },
		func(m *model) { m.profile = "poster" },
		func(m *model) { m.directory += "/other" },
	} {
		other := m
		change(&other)
		if other.hasFullAccess() || other.permitsAction("Run command") {
			t.Fatal("another account, profile or folder inherited session consent")
		}
	}
	fresh := model{user: m.user, profile: m.profile, directory: m.directory}
	if fresh.permitsAction("Run command") {
		t.Fatal("a new session inherited consent")
	}
	m.setPermissionMode(0)
	if captured.permitsAction("") || captured.permitsAction("Run command") {
		t.Fatal("permission reset failed to revoke the running callback's consent")
	}
}

func TestAllowAllMouseHandlesVisibleChoice(t *testing.T) {
	for _, size := range [][2]int{{48, 16}, {80, 24}, {120, 36}} {
		m, _ := workspaceApprovalFixture(t)
		m.width, m.height = size[0], size[1]
		rows, footer := m.actionApprovalRows()
		panel, sources := m.bottomPanelRows(rows, footer)
		found := false
		for y, source := range sources {
			if source != 5+approvalAll {
				continue
			}
			found = true
			click, answer := workspaceApprovalFixture(t)
			click.width, click.height = size[0], size[1]
			next, _ := click.Update(tea.MouseClickMsg{Button: tea.MouseLeft, X: 5, Y: size[1] - len(panel) + y})
			click = next.(model)
			select {
			case yes := <-answer:
				if !yes || !click.hasFullAccess() {
					t.Fatal("Allow all click failed to approve session actions")
				}
			default:
				t.Fatalf("Allow all click was lost at %v", size)
			}
		}
		if !found {
			t.Fatalf("Allow all was not visible at %v", size)
		}
	}
}

func TestAllowAllDoesNotGrantWhenAnswerCannotBeDelivered(t *testing.T) {
	for _, mode := range []int{0, 1, 3} {
		m, answer := workspaceApprovalFixture(t)
		m.permissions.remembered = &sync.Map{}
		m.setPermissionMode(mode)
		answer <- false
		m.localAgent.approvalChoice = approvalAll
		next, _ := m.updateActionApproval(tea.KeyPressMsg{Code: tea.KeyEnter})
		m = next.(model)
		if m.hasFullAccess() || m.permissionMode() != mode || m.permitsAction("Run command") {
			t.Fatal("undelivered decision granted permissions")
		}
	}
}

func TestApprovalGuidanceDeclinesAndReachesModel(t *testing.T) {
	note := ""
	answer := make(chan bool, 1)
	m := model{localAgent: localAgentState{busy: true, approval: &providers.Approval{Action: "Run command"}, answer: answer, approvalGuidance: &note, approvalEditing: true, approvalInput: textField{value: "Avoid sudo; use the existing toolchain"}}}
	next, _ := m.updateActionApproval(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = next.(model)
	if <-answer || note != "Avoid sudo; use the existing toolchain" || m.localAgent.approval != nil || !m.localAgent.busy {
		t.Fatal("guidance did not decline and resume model")
	}
}
func TestAllowMenuWorksDuringPendingApproval(t *testing.T) {
	m := model{localAgent: localAgentState{busy: true, approval: &providers.Approval{Action: "Run command"}}}
	next, _ := m.openPermissions()
	if !next.(model).permissions.open || next.(model).localAgent.approval == nil {
		t.Fatal("permission controls blocked or lost pending action")
	}
}
func TestPermissionRevocationReachesRunningApprovalCallback(t *testing.T) {
	m := model{permissions: localPermissions{remembered: &sync.Map{}}}
	captured := m
	m.setPermissionMode(2)
	if !captured.permitsAction("Run command") {
		t.Fatal("running callback missed new permission")
	}
	m.setPermissionMode(3)
	if captured.permitsAction("Run command") {
		t.Fatal("running callback missed revocation")
	}
}

func TestAllowAllIsVisibleToCallbackBeforeAnswer(t *testing.T) {
	for range 100 {
		m, answer := workspaceApprovalFixture(t)
		m.permissions.remembered = &sync.Map{}
		captured := m
		permitted := make(chan bool, 1)
		go func() {
			if <-answer {
				permitted <- captured.permitsAction("Download from remote task") && captured.permitsAction("Run command")
			}
		}()
		m.localAgent.approvalChoice = approvalAll
		m.updateActionApproval(tea.KeyPressMsg{Code: tea.KeyEnter})
		select {
		case allowed := <-permitted:
			if !allowed {
				t.Fatal("running callback could ask again immediately after Allow all")
			}
		case <-time.After(time.Second):
			t.Fatal("Allow all did not answer the running callback")
		}
	}
}

func TestArrivingApprovalTakesFocusAndOwnsBoardClicks(t *testing.T) {
	m, answer := workspaceApprovalFixture(t)
	approval := m.localAgent.approval
	m.localAgent.approval = nil
	m.commands.open, m.shortcutsOpen, m.board.open = true, true, true
	next, _ := m.Update(localAgentUpdate{approval: approval, answer: answer})
	m = next.(model)
	if m.commands.open || m.shortcutsOpen || m.workAgent.status != "Waiting for your approval" ||
		!strings.Contains(ansi.Strip(m.View().Content), "Allow all for this session") {
		t.Fatal("approval was hidden behind a temporary overlay")
	}
	rows, footer := m.actionApprovalRows()
	panel, sources := m.bottomPanelRows(rows, footer)
	for y, source := range sources {
		if source == 5+approvalAll {
			next, _ = m.Update(tea.MouseClickMsg{Button: tea.MouseLeft, X: 5, Y: m.height - len(panel) + y})
			m = next.(model)
			break
		}
	}
	select {
	case yes := <-answer:
		if !yes || !m.hasFullAccess() || !m.board.open {
			t.Fatal("approval click changed the underlying board")
		}
	default:
		t.Fatal("board swallowed the visible approval click")
	}
}

func TestApprovalGuidancePasteTakesPriorityOverBoard(t *testing.T) {
	m, _ := workspaceApprovalFixture(t)
	m.board = workBoard{open: true, action: "create", fields: []textField{{value: "Keep board input"}}}
	m.localAgent.approvalEditing = true
	next, _ := m.Update(tea.PasteMsg{Content: "Use a different command"})
	m = next.(model)
	if m.localAgent.approvalInput.value != "Use a different command" || m.board.fields[0].value != "Keep board input" {
		t.Fatal("approval guidance was pasted into the hidden board")
	}
}

func TestFullAccessPermissionConfirmationResolvesPendingApproval(t *testing.T) {
	for _, accept := range []bool{true, false} {
		m, answer := workspaceApprovalFixture(t)
		next, _ := m.openPermissions()
		m = next.(model)
		m.permissions.selection = 2
		next, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
		m = next.(model)
		key := 'n'
		if accept {
			key = 'y'
		}
		next, _ = m.Update(tea.KeyPressMsg{Code: key, Text: string(key)})
		m = next.(model)
		select {
		case yes := <-answer:
			if !accept || !yes || !m.hasFullAccess() || m.localAgent.approval != nil {
				t.Fatal("permission confirmation answered the approval incorrectly")
			}
		default:
			if accept || m.hasFullAccess() || m.localAgent.approval == nil {
				t.Fatal("Full Access required an extra popup or No granted permissions")
			}
		}
	}
}

func TestStreamingUpdatesCannotSwallowApproval(t *testing.T) {
	m := streamingFixture(t)
	answer := make(chan bool, 1)
	m.localAgent.events <- localAgentUpdate{sequence: 4, event: providers.Event{Type: "assistant_delta", Text: "Preparing the command"}}
	m.localAgent.events <- localAgentUpdate{sequence: 4, approval: &providers.Approval{Action: "Run command", Content: "go test ./..."}, answer: answer}
	msg := m.waitLocalAgent()().(localAgentUpdate)
	if msg.approval == nil || len(msg.leading) != 1 || msg.leading[0].Text != "Preparing the command" {
		t.Fatal("stream batching swallowed the approval or preceding text")
	}
	next, _ := m.Update(msg)
	m = next.(model)
	if !strings.Contains(ansi.Strip(m.View().Content), "Allow all for this session") {
		t.Fatal("streamed approval did not show its decision panel")
	}
	_, _ = m.Update(tea.KeyPressMsg{Code: 'y', Text: "y"})
	select {
	case yes := <-answer:
		if !yes {
			t.Fatal("streamed approval received the wrong decision")
		}
	default:
		t.Fatal("streamed approval did not reach the waiting provider")
	}
}
