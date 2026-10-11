package tui

import (
	tea "charm.land/bubbletea/v2"
	"context"
	"github.com/rajandhamala/Maruvo/cli/internal/api"
	"github.com/rajandhamala/Maruvo/cli/internal/providers"
	"strings"
	"testing"
)

func TestInvitationAcceptanceOpensWorkspaceWithoutStartingModel(t *testing.T) {
	worker := int64(1)
	invitation := workInvitation{PostID: 5, Nonce: strings.Repeat("a", 32)}
	m := model{ctx: context.Background(), screen: feedScreen, user: api.User{ID: "3"}, invitations: inviteState{pending: &invitation, generation: 4}, directory: t.TempDir()}
	next, cmd := m.Update(inviteWorkspace{generation: 4, invitation: invitation, info: api.PostInfo{Post: api.Post{ID: 5, UserID: 3, AcceptedBy: &worker}}})
	m = next.(model)
	if cmd == nil || m.screen != workspaceScreen || m.workspacePendingAction != "work-invite" || m.workAgent.active || m.localAgent.busy || m.invitations.accepted == nil {
		t.Fatal("accept failed to navigate or started work before preparation")
	}
}

func TestDeclinedInvitationCannotNavigateFromLateResponse(t *testing.T) {
	m := model{screen: feedScreen, invitations: inviteState{generation: 4}}
	next, cmd := m.Update(inviteWorkspace{generation: 4, invitation: workInvitation{PostID: 5, Nonce: strings.Repeat("a", 32)}})
	if cmd != nil || next.(model).screen != feedScreen {
		t.Fatal("cancelled invitation navigated from late request")
	}
}

func TestIncomingInvitationOwnsKeyRouting(t *testing.T) {
	invitation := workInvitation{PostID: 5, Nonce: strings.Repeat("a", 32)}
	m := model{remote: remoteState{open: true}, invitations: inviteState{pending: &invitation}}
	next, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	if next.(model).invitations.pending != nil || !next.(model).remote.open {
		t.Fatal("invitation keys went to hidden remote menu")
	}
}

func TestCancelledInvitationClosesPromptAndPreventsLateAccept(t *testing.T) {
	nonce := strings.Repeat("a", 32)
	invitation := workInvitation{PostID: 5, Nonce: nonce}
	m := model{invitations: inviteState{pending: &invitation, accepted: &invitation}, workspacePendingAction: "work-invite"}
	m.applyInvitation(workInvitation{PostID: 5, Nonce: strings.Repeat("b", 32), Cancelled: true})
	if m.invitations.pending == nil {
		t.Fatal("stale cancellation dismissed current invitation")
	}
	m.applyInvitation(workInvitation{PostID: 5, Nonce: nonce, Cancelled: true})
	if m.invitations.pending != nil || m.invitations.accepted != nil || m.workspacePendingAction != "" {
		t.Fatal("cancelled invitation remained actionable")
	}
}

func TestRemoteStopCancelsMatchingAgentOnly(t *testing.T) {
	nonce := strings.Repeat("a", 32)
	m := model{workAgent: workspaceAgent{active: true, postID: 5}, workSetup: workSetup{postID: 5, pair: nonce + ":" + strings.Repeat("b", 32)}}
	m.applyInvitation(workInvitation{PostID: 6, Nonce: nonce, Cancelled: true})
	if !m.workAgent.active {
		t.Fatal("another task stopped this session")
	}
	m.applyInvitation(workInvitation{PostID: 5, Nonce: nonce, Cancelled: true})
	if m.workAgent.active || m.workSetup.autoReady {
		t.Fatal("remote stop did not cancel local work")
	}
}

func TestRemoteStopAllowsNewInvitationAcceptance(t *testing.T) {
	nonce := strings.Repeat("a", 32)
	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)
	answer := make(chan bool, 1)
	m := model{
		ctx: t.Context(), screen: workspaceScreen,
		workAgent: workspaceAgent{active: true, pending: true, postID: 13, generation: 2},
		workSetup: workSetup{armed: true, postID: 13, pair: nonce + ":" + strings.Repeat("b", 32)},
		localAgent: localAgentState{
			open: true, busy: true, sequence: 7, ctx: ctx, cancel: cancel,
			approval: &providers.Approval{Action: "Run command"}, answer: answer,
		},
	}
	m.localAgent.applyEvent(providers.Event{Type: "assistant_delta", Text: "Incomplete work"})
	m.applyInvitation(workInvitation{PostID: 13, Nonce: nonce, Cancelled: true})
	if ctx.Err() == nil || m.workAgent.active || m.workAgent.pending || m.workSetup.armed ||
		m.localAgent.open || m.localAgent.busy || m.localAgent.approval != nil || m.localAgent.answer != nil {
		t.Fatal("remote stop left the cancelled agent or its approval active")
	}
	if m.localAgent.streaming || !strings.Contains(m.localAgent.lines[0], "incomplete answer") {
		t.Fatal("remote stop did not mark the partial response as interrupted")
	}
	select {
	case <-answer:
		t.Fatal("remote stop answered the pending approval")
	default:
	}

	m.applyInvitation(workInvitation{PostID: 13, Nonce: strings.Repeat("c", 32)})
	next, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = next.(model)
	if cmd == nil || !m.invitations.busy || m.invitations.pending == nil {
		t.Fatal("Enter could not accept a fresh invitation after remote stop")
	}

	m.localAgent.open, m.localAgent.busy = true, true
	for _, msg := range []tea.Msg{
		localAgentReady{sequence: 7, label: "old model"},
		localAgentUpdate{sequence: 7, approval: &providers.Approval{Action: "Old command"}, answer: answer},
		localAgentUpdate{sequence: 7, done: true, err: context.Canceled},
	} {
		next, cmd = m.Update(msg)
		m = next.(model)
		if cmd != nil || !m.localAgent.busy || m.localAgent.approval != nil ||
			m.localAgent.err != nil || m.localAgent.label == "old model" {
			t.Fatal("cancelled session changed the next agent session")
		}
	}
}

func TestPeerStatusCannotBecomeAnInvitationOrChangeAnotherSession(t *testing.T) {
	nonce := strings.Repeat("a", 32)
	m := model{workAgent: workspaceAgent{active: true, postID: 5}, workSetup: workSetup{postID: 5, pair: strings.Repeat("b", 32) + ":" + nonce}}
	m.applyInvitation(workInvitation{PostID: 5, Nonce: nonce, Status: "waiting_for_approval", Detail: "Waiting for local action approval"})
	if m.workSetup.peerStatus != "waiting_for_approval" || m.invitations.pending != nil || m.workAgent.pending {
		t.Fatal("status did not update current peer, opened an invitation or woke the model")
	}
	m.applyInvitation(workInvitation{PostID: 5, Nonce: strings.Repeat("c", 32), Status: "working"})
	if m.workSetup.peerStatus != "waiting_for_approval" {
		t.Fatal("stale session overwrote peer status")
	}
}

func TestBothRolesExposeApprovalAndPauseState(t *testing.T) {
	m := model{workAgent: workspaceAgent{active: true}}
	if status, _ := m.workStatus(); status != "listening" {
		t.Fatal(status)
	}
	m.localAgent.busy = true
	if status, _ := m.workStatus(); status != "working" {
		t.Fatal(status)
	}
	m.localAgent.approval = &providers.Approval{Action: "Run command"}
	if status, _ := m.workStatus(); status != "waiting_for_approval" {
		t.Fatal(status)
	}
	m.workAgent.paused = true
	if status, _ := m.workStatus(); status != "paused" {
		t.Fatal(status)
	}
}

func TestInvitationSelectionDeclinesWithEnter(t *testing.T) {
	invitation := workInvitation{PostID: 11, Nonce: strings.Repeat("a", 32)}
	m := model{invitations: inviteState{pending: &invitation}}
	next, _ := m.updateInvite(tea.KeyPressMsg{Code: tea.KeyDown})
	m = next.(model)
	next, cmd := m.updateInvite(tea.KeyPressMsg{Code: tea.KeyEnter})
	if next.(model).invitations.pending != nil || cmd == nil {
		t.Fatal("selected Decline was not confirmed with Enter")
	}
}
