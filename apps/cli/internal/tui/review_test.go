package tui

import (
	"encoding/json"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/rajandhamala/Maruvo/cli/internal/api"
)

func reviewerModel() model {
	return model{screen: workspaceScreen, workspaceReview: true, workspaceGen: 1, workspace: api.Workspace{
		Post:      api.Post{ID: 1, Status: "in_progress"},
		Escrow:    api.Escrow{State: "confirmed"},
		CanReview: true,
		State:     api.WorkspaceState{ReviewState: "submitted", SubmissionVersion: 2},
	}}
}

func TestReviewDecisionKeepsOpenedVersion(t *testing.T) {
	for _, action := range []string{"a", "x", "e"} {
		t.Run(action, func(t *testing.T) {
			m := reviewerModel()
			next, _ := m.updateWorkspace(tea.KeyPressMsg{Code: rune(action[0]), Text: action})
			m = next.(model)
			m.workspaceInput.value = "Reviewed version 2"
			m.workspace.State.SubmissionVersion = 3
			next, cmd := m.performWorkspaceAction()

			m = next.(model)
			if cmd != nil || m.err == nil {
				t.Fatal("a decision for version 2 must not send a request for version 3")
			}
		})
	}
}

func TestLiveReviewChangeDismissesDecision(t *testing.T) {
	for _, event := range []api.WorkspaceEvent{
		{Kind: "work.submitted", Data: json.RawMessage(`{"submission_version":3,"note":"New delivery"}`)},
		{Kind: "review.changes_requested", Data: json.RawMessage(`{"note":"Revise this delivery"}`)},
		{Kind: "review.completed", Data: json.RawMessage(`{"review_state":"approved"}`)},
	} {
		t.Run(event.Kind, func(t *testing.T) {
			m := reviewerModel()
			next, _ := m.updateWorkspace(tea.KeyPressMsg{Code: 'a', Text: "a"})
			m = next.(model)
			m.workspaceInput.value, m.reviewConfirm = "Review note", true
			event.PostID, event.ID = 1, 1
			m.applyWorkspaceEvent(event)

			if m.workspaceAction != "" || m.reviewConfirm {
				t.Fatal("a live delivery change must dismiss the stale decision")
			}
		})
	}
}

func TestStalePreparedDecisionCannotConfirmOrSign(t *testing.T) {
	m := reviewerModel()
	next, _ := m.updateWorkspace(tea.KeyPressMsg{Code: 'a', Text: "a"})
	m = next.(model)
	plan := api.SettlementPlan{Settlement: api.Settlement{State: "prepared", SubmissionVersion: 2}}
	m.workspace.State.SubmissionVersion = 3
	m.loading = true
	next, cmd := m.reviewPrepared(reviewPrepared{generation: 1, plan: plan})

	m = next.(model)
	if cmd != nil || m.reviewConfirm || m.loading || m.err == nil {
		t.Fatal("a delayed preparation response must not confirm an older delivery")
	}

	m.reviewPlan, m.reviewConfirm = plan, true
	next, cmd = m.signSettlement()

	m = next.(model)
	if cmd != nil || m.reviewConfirm || m.err == nil {
		t.Fatal("a stale delivery must be rejected before loading a key or signing")
	}
}
