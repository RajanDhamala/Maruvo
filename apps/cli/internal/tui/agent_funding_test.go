package tui

import (
	"context"
	"github.com/rajandhamala/Maruvo/cli/internal/api"
	"github.com/rajandhamala/Maruvo/cli/internal/providers"
	"testing"
)

func TestAgentFundingHandoffPreservesChatAndRequiresHumanSigning(t *testing.T) {
	m := model{user: api.User{ID: "3"}, localAgent: localAgentState{open: true, sequence: 8, lines: []string{"saved reply"}}}
	next, _ := m.Update(localAgentUpdate{sequence: 7, event: providers.Event{Type: "open_funding", PostID: 4}})
	m = next.(model)
	if m.localAgent.fundingTask != 0 {
		t.Fatal("stale turn queued funding")
	}
	next, _ = m.Update(localAgentUpdate{sequence: 8, event: providers.Event{Type: "open_funding", PostID: 4}})
	m = next.(model)
	if m.localAgent.fundingTask != 4 || m.fundingConfirm {
		t.Fatal("handoff must queue without signing")
	}
	worker := int64(1)
	info := api.PostInfo{Post: api.Post{ID: 4, UserID: 3, AcceptedBy: &worker, Status: "negotiating"}, Escrow: api.Escrow{State: "unfunded"}}
	next, cmd := m.Update(agentFundingLoaded{info: info})
	m = next.(model)
	if cmd == nil || m.screen != detailScreen || m.posts[0].ID != 4 || m.fundingConfirm || len(m.localAgent.lines) != 1 {
		t.Fatal("did not preserve chat and prepare selected task for human review")
	}
}

func TestAgentWorkspaceHandoffPreservesChatAndDraft(t *testing.T) {
	worker := int64(1)
	post := api.Post{ID: 4, UserID: 3, AcceptedBy: &worker, Status: "in_progress"}
	m := model{user: api.User{ID: "1"}, ctx: context.Background(), localAgent: localAgentState{open: true, sequence: 8, lines: []string{"saved reply"}}, workspace: api.Workspace{Post: post}, composer: chatComposer{root: "/tmp", draft: textField{value: "existing draft"}}}
	next, _ := m.Update(localAgentUpdate{sequence: 7, event: providers.Event{Type: "open_workspace", PostID: 4}})
	m = next.(model)
	if m.localAgent.workspaceTask != 0 {
		t.Fatal("stale turn queued workspace")
	}
	next, _ = m.Update(localAgentUpdate{sequence: 8, event: providers.Event{Type: "open_workspace", PostID: 4}})
	m = next.(model)
	if m.localAgent.workspaceTask != 4 {
		t.Fatal("missing workspace request")
	}
	next, cmd := m.Update(agentWorkspaceLoaded{info: api.PostInfo{Post: post, Escrow: api.Escrow{State: "confirmed"}}})
	m = next.(model)
	if cmd == nil || m.screen != workspaceScreen || m.posts[0].ID != 4 || m.localAgent.open || len(m.localAgent.lines) != 1 || m.composer.draft.value != "existing draft" {
		t.Fatal("workspace handoff lost chat or draft")
	}
	m.stopWorkspace()
}
