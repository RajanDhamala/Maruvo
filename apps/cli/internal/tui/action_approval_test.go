package tui

import (
	"strings"
	"sync"
	"testing"

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
		{"select yes", []rune{tea.KeyUp, tea.KeyUp, tea.KeyEnter}, true},
		{"yes shortcut", []rune{'y'}, true},
		{"escape declines", []rune{tea.KeyEscape}, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			answer := make(chan bool, 1)
			m := model{localAgent: localAgentState{open: true, busy: true, approvalChoice: 2, approval: &providers.Approval{Action: "Run command", Path: "/tmp/project", Content: "go test ./..."}, answer: answer}}
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

func TestActionApprovalShowsChoicesAndExactCommand(t *testing.T) {
	m := model{width: 80, height: 24, localAgent: localAgentState{open: true, approvalChoice: 2, approval: &providers.Approval{Action: "Run command", Path: "/tmp/project", Content: "go test ./..."}}}
	view := ansi.Strip(m.actionApprovalView().Content)
	for _, text := range []string{"Allow this action?", "Yes", "No", "go test ./...", "/tmp/project"} {
		if !strings.Contains(view, text) {
			t.Fatalf("missing %s in %s", text, view)
		}
	}
}

func TestRememberedApprovalCoversActionTypeAndIsAccountScoped(t *testing.T) {
	action := providers.Approval{Action: "Run command", Path: "/tmp/demo", Content: "go test ./..."}
	answer := make(chan bool, 1)
	m := model{profile: "default", user: api.User{ID: "1"}, directory: t.TempDir(), permissions: localPermissions{remembered: &sync.Map{}}, localAgent: localAgentState{approval: &action, answer: answer, approvalChoice: 1}}
	next, _ := m.updateActionApproval(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = next.(model)
	if !<-answer || !m.rememberedApproval(action) {
		t.Fatal("remember selection did not approve and retain exact action")
	}
	changed := action
	changed.Content = "rm -rf demo"
	if !m.rememberedApproval(changed) {
		t.Fatal("another command still asked after allowing command actions")
	}
	changed.Action = "Upload file"
	if m.rememberedApproval(changed) {
		t.Fatal("another action type inherited approval")
	}
	m.user.ID = "3"
	if m.rememberedApproval(action) {
		t.Fatal("another account inherited approval")
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
