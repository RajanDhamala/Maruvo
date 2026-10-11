package tui

import (
	"encoding/json"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/rajandhamala/Maruvo/cli/internal/api"
)

func TestAccessReceiptDismissesPreparedRefundAndKeepsOtherActions(t *testing.T) {
	m := reviewerModel()
	m.workspaceAction, m.reviewConfirm, m.reviewVersion = "x", true, 2
	m.reviewPlan.Settlement = api.Settlement{Action: "refund", State: "prepared", SubmissionVersion: 2}
	if !m.applyWorkspaceEvent(api.WorkspaceEvent{PostID: 1, ID: 1, Kind: "file.accessed", Data: json.RawMessage(`{"name":"patch.diff","refund_blocked":true}`)}) {
		t.Fatal("access event was ignored")
	}
	if !m.workspace.State.RefundBlocked || m.reviewConfirm || m.workspaceAction != "" || m.err == nil {
		t.Fatal("access left the old refund signing screen active")
	}
	if m.canReviewAction("x") || !m.canReviewAction("a") || !m.canReviewAction("e") {
		t.Fatal("access must disable refund while retaining approval and revisions")
	}
	next, cmd := m.updateWorkspace(tea.KeyPressMsg{Code: 'x', Text: "x"})
	m = next.(model)
	if cmd != nil || m.workspaceAction != "" || m.err == nil {
		t.Fatal("refund shortcut bypassed access guard")
	}
	m.workspaceAction, m.reviewConfirm = "x", true
	_, cmd = m.signSettlement()
	if cmd != nil {
		t.Fatal("accessed delivery reached key loading or signing")
	}
	m.applyWorkspaceEvent(api.WorkspaceEvent{PostID: 1, ID: 2, Kind: "file.accessed", Data: json.RawMessage(`{"refund_blocked":false}`)})
	if !m.workspace.State.RefundBlocked {
		t.Fatal("another receipt cleared protection")
	}
}

func TestProtectedReviewFitsAndRetainsPaymentControls(t *testing.T) {
	for _, size := range [][2]int{{48, 16}, {80, 24}, {120, 36}, {168, 48}} {
		m := deliveryReviewFixture()
		m.width, m.height = size[0], size[1]
		m.workspace.State.RefundBlocked = true
		for _, hit := range m.reviewButtons().hits {
			if hit.action == "x" {
				t.Fatal("protected review exposed a refund button")
			}
		}
		l := m.postsLayout()
		controls := map[string]bool{"a": false, "e": false, "workspace-tab": false}
		for _, hit := range l.hits {
			if _, ok := controls[hit.action]; ok {
				controls[hit.action] = true
			}
		}
		for action, present := range controls {
			if !present {
				t.Fatalf("%v hid %s", size, action)
			}
		}
		view := m.View().Content
		if !strings.Contains(ansi.Strip(view), "Refund blocked") {
			t.Fatalf("%v hid refund reason", size)
		}
		for _, row := range strings.Split(view, "\n") {
			if ansi.StringWidth(row) > size[0] {
				t.Fatalf("%v overflowed terminal width", size)
			}
		}
		if len(strings.Split(view, "\n")) != size[1] {
			t.Fatal("protected review overflowed terminal height")
		}
	}
}

func TestDownloadExplainsAccessBeforeTransfer(t *testing.T) {
	m := deliveryReviewFixture()
	m.width, m.height = 120, 36
	m.user.ID = "1"
	worker := int64(2)
	m.workspace.Post.AcceptedBy = &worker
	m.workspace.Files[2].UploadedBy, m.workspace.Files[2].Purpose = worker, "output"
	next, cmd := m.downloadWorkspaceFile(2)
	m = next.(model)
	if cmd != nil || m.workspaceAction != "d" || m.loading || m.workspace.State.RefundBlocked {
		t.Fatal("opening download recorded access or ran a transfer")
	}
	view := strings.Join(strings.Fields(ansi.Strip(strings.Join(m.postsLayout().rows, "\n"))), " ")
	if !strings.Contains(view, "downloading blocks unilateral refunds in Maruvo") || !strings.Contains(view, "does not approve payment") {
		t.Fatal("download omitted the consequence before confirmation")
	}
}
