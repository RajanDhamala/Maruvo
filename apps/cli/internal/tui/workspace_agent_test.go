package tui

import (
	"context"
	"testing"
	"time"

	"github.com/rajandhamala/Maruvo/cli/internal/api"
	"github.com/rajandhamala/Maruvo/cli/internal/providers"
)

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
