package tui

import (
	tea "charm.land/bubbletea/v2"
	"errors"
	"github.com/rajandhamala/Maruvo/cli/internal/api"
	"testing"
)

func TestWorkPreparationArmsInvitationWithoutAnotherReadyClick(t *testing.T) {
	m := model{screen: workspaceScreen, workspace: api.Workspace{Post: api.Post{ID: 5}, Escrow: api.Escrow{State: "confirmed"}}, workSetup: workSetup{open: true, autoReady: true, invite: true, postID: 5, generation: 3}}
	next, cmd := m.workSetupResult(workSetupResult{generation: 3, provider: "fixture/model"})
	m = next.(model)
	if cmd == nil || !m.workSetup.armed || !m.workSetup.invite || m.workAgent.active {
		t.Fatal("/work did not prepare an invitation or started before acceptance")
	}
}

func TestAcceptPreparationKeepsInvitationBinding(t *testing.T) {
	m := model{screen: workspaceScreen, workspace: api.Workspace{Post: api.Post{ID: 5}, Escrow: api.Escrow{State: "confirmed"}}, workSetup: workSetup{autoReady: true, postID: 5, generation: 3, target: "sender-session"}}
	next, cmd := m.workSetupResult(workSetupResult{generation: 3, provider: "fixture/model", readiness: api.AgentReadiness{Peer: true}})
	m = next.(model)
	if cmd == nil || !m.workSetup.armed || m.workSetup.invite || m.workSetup.target != "sender-session" || m.workAgent.active {
		t.Fatal("accept lost sender binding or invited sender again")
	}
}

func TestCancelPreparationPreventsLateInvitation(t *testing.T) {
	m := model{screen: workspaceScreen, workspace: api.Workspace{Post: api.Post{ID: 5}}, workSetup: workSetup{open: true, busy: true, autoReady: true, generation: 3, postID: 5}}
	next, _ := m.updateWorkSetup(tea.KeyPressMsg{Code: 'n', Text: "n"})
	m = next.(model)
	next, cmd := m.workSetupResult(workSetupResult{generation: 3, provider: "fixture/model"})
	m = next.(model)
	if cmd != nil || m.workSetup.armed || m.workSetup.autoReady || m.workSetup.open {
		t.Fatal("cancelled preparation sent an invitation")
	}
}

func TestReadinessRequiresExplicitConsentAndFunding(t *testing.T) {
	worker := int64(1)
	m := model{screen: workspaceScreen, user: api.User{ID: "1"}, workspace: api.Workspace{Post: api.Post{ID: 5, UserID: 3, AcceptedBy: &worker}}, workSetup: workSetup{open: true, prepared: true, postID: 5, generation: 7}}
	next, _ := m.workSetupResult(workSetupResult{generation: 7, readiness: api.AgentReadiness{You: true, Peer: true, Pair: "old:session"}})
	m = next.(model)
	if m.workAgent.active || m.workSetup.armed {
		t.Fatal("server snapshot started work without local consent")
	}
	next, cmd := m.updateWorkSetup(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = next.(model)
	if cmd != nil || m.workSetup.armed || m.workSetup.err == nil {
		t.Fatal("unfunded task became ready")
	}
}

func TestReadinessLossStopsWorkAndStaleResponsesAreIgnored(t *testing.T) {
	m := model{screen: workspaceScreen, workspace: api.Workspace{Post: api.Post{ID: 5}}, workSetup: workSetup{armed: true, postID: 5, generation: 8, pair: "a:b"}, workAgent: workspaceAgent{active: true, postID: 5}}
	next, _ := m.workSetupResult(workSetupResult{generation: 7})
	if !next.(model).workAgent.active {
		t.Fatal("stale response stopped current session")
	}
	next, _ = m.workSetupResult(workSetupResult{generation: 8, readiness: api.AgentReadiness{You: true}})
	m = next.(model)
	if m.workAgent.active || m.workSetup.armed {
		t.Fatal("peer consent loss left work running")
	}
}

func TestFailedWorkSetupCanRetry(t *testing.T) {
	worker := int64(1)
	m := model{screen: workspaceScreen, user: api.User{ID: "3"}, workspace: api.Workspace{Post: api.Post{ID: 11, UserID: 3, AcceptedBy: &worker}}, workSetup: workSetup{open: true, prepared: true, postID: 11, generation: 7, instruction: "Start demo", err: errors.New("invalid JSON payload")}}
	next, cmd := m.updateWorkSetup(tea.KeyPressMsg{Code: 'r', Text: "r"})
	got := next.(model)
	if cmd == nil || !got.workSetup.busy || !got.workSetup.autoReady || !got.workSetup.invite || got.workSetup.err != nil || got.workSetup.generation <= 7 || got.workSetup.instruction != "Start demo" || got.workAgent.active {
		t.Fatal("retry did not restart preparation without starting the agent")
	}
}

func TestSentInvitationReturnsToWorkspaceWhileWaiting(t *testing.T) {
	m := model{screen: workspaceScreen, workspace: api.Workspace{Post: api.Post{ID: 11}}, workSetup: workSetup{open: true, armed: true, postID: 11, generation: 4}}
	next, cmd := m.workSetupResult(workSetupResult{generation: 4, readiness: api.AgentReadiness{You: true}})
	got := next.(model)
	if cmd == nil || got.workSetup.open || !got.workSetup.armed || got.workAgent.active || got.notice == "" {
		t.Fatal("sent invitation did not return to workspace while preserving readiness")
	}
}
