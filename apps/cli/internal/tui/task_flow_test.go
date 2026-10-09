package tui

import (
	"strconv"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/rajandhamala/Maruvo/cli/internal/api"
)

func TestPaidLabelRequiresConfirmedEscrowRelease(t *testing.T) {
	worker := int64(2)
	post := api.Post{ID: 5, UserID: 3, AcceptedBy: &worker, Status: "completed"}
	m := model{user: api.User{ID: "3"}}
	for _, state := range []string{"", "confirmed", "pending"} {
		flow := m.taskFlow(post, api.Escrow{State: state}, "approved", api.Settlement{}, true)
		if strings.Contains(flow.stage, "paid") {
			t.Fatalf("unreleased escrow %q labelled as paid", state)
		}
	}
	flow := m.taskFlow(post, api.Escrow{State: "released"}, "approved", api.Settlement{}, true)
	if !strings.Contains(flow.stage, "worker paid") {
		t.Fatal("confirmed release not reflected", flow.stage)
	}
}

func TestTaskFlowRolesAndPaymentStates(t *testing.T) {
	worker := int64(2)
	for _, tc := range []struct {
		name, user, status, escrow, review, settlement, action, contains string
		reviewer                                                         bool
	}{
		{"requester funding", "1", "negotiating", "", "working", "", "b", "sign funding", false},
		{"worker waiting", "2", "negotiating", "", "working", "", "b", "requester must fund", false},
		{"pending funding", "1", "negotiating", "pending", "working", "", "b", "Wait for confirmation", false},
		{"deliver", "2", "in_progress", "confirmed", "working", "", "s", "submit it for review", false},
		{"requester waiting", "1", "in_progress", "confirmed", "working", "", "v", "Share any needed inputs", false},
		{"revision", "2", "in_progress", "confirmed", "changes_requested", "", "s", "submit it again", false},
		{"worker review", "2", "in_progress", "confirmed", "submitted", "", "v", "authorized reviewer", false},
		{"reviewer", "3", "in_progress", "confirmed", "submitted", "", "v", "request changes", true},
		{"decision unsigned", "3", "in_progress", "confirmed", "submitted", "prepared", "v", "must sign", true},
		{"pending settlement", "2", "in_progress", "confirmed", "submitted", "pending", "v", "Wait for confirmation", false},
		{"paid", "2", "completed", "released", "approved", "", "", "payment details", false},
		{"refunded", "1", "cancelled", "refunded", "refunded", "", "", "returned", false},
		{"cancelled", "2", "cancelled", "", "working", "", "", "assignment is closed", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := chatModel()
			m.user.ID = tc.user
			m.workspace.Post = api.Post{ID: 1, UserID: 1, AcceptedBy: &worker, Status: tc.status}
			m.workspace.Escrow.State = tc.escrow
			m.workspace.State.ReviewState = tc.review
			m.workspace.Settlement.State = tc.settlement
			m.workspace.CanReview = tc.reviewer
			flow := m.workspaceFlow()
			if flow.action != tc.action || !strings.Contains(flow.next, tc.contains) {
				t.Fatalf("incorrect next action: %+v", flow)
			}
		})
	}
}

func TestDeliveryActionVisibleAndOpensWithoutSubmitting(t *testing.T) {
	worker := int64(2)
	for _, size := range [][2]int{{48, 16}, {80, 24}, {120, 36}} {
		m := chatModel()
		m.width, m.height = size[0], size[1]
		m.token, m.user.ID = "session", "2"
		m.workspace.Post.UserID, m.workspace.Post.AcceptedBy = 1, &worker
		m.workspace.Escrow.State = "confirmed"
		m.composer.draft.value = "Keep my chat draft"
		layout := m.chatLayout()
		if len(layout.rows) > m.bodyHeight() {
			t.Fatalf("%v: flow pushes composer out of view", size)
		}
		found := false
		for _, hit := range layout.hits {
			if hit.action != "s" {
				continue
			}
			found = true
			next, cmd := m.updateMouse(tea.MouseClickMsg{Button: tea.MouseLeft, X: m.contentX() + hit.x, Y: m.bodyStart() + hit.y})
			result := next.(model)
			if cmd != nil || result.workspaceAction != "s" || result.composer.draft.value != "Keep my chat draft" {
				t.Fatal("delivery button must open submission without sending or changing the chat draft")
			}
		}
		if !found {
			t.Fatalf("%v: missing visible submit action", size)
		}
		for _, row := range strings.Split(m.View().Content, "\n") {
			if ansi.StringWidth(row) > size[0] {
				t.Fatalf("%v: row overflow", size)
			}
		}
	}
}

func TestDetailFlowAndActionsFitSmallTerminal(t *testing.T) {
	worker := int64(2)
	m := model{width: 48, height: 16, screen: detailScreen, user: api.User{ID: "1"}, posts: []api.Post{
		{ID: 1, UserID: 1, AcceptedBy: &worker, Status: "negotiating", EndTime: time.Now().Add(time.Hour)},
	}}
	if len(m.detailHeader().rows) >= m.bodyHeight() {
		t.Fatal("task header must leave room for details")
	}
	if !strings.Contains(plain(strings.Join(m.detailRows(), "\n")), "sign funding") {
		t.Fatal("missing next step on small terminal")
	}
	for _, id := range []int64{1, 2} {
		m.user.ID = strconv.FormatInt(id, 10)
		m.posts[0].AcceptedBy = nil
		flow := m.taskFlow(m.posts[0], api.Escrow{}, "", api.Settlement{}, false)
		if !strings.Contains(flow.stage, "Step 1/4") {
			t.Fatal("open task must start at acceptance")
		}
	}
}

func TestCreateTaskButtonStartsDraftWithoutPublishing(t *testing.T) {
	m := dashboardFixture()
	m.homeInput = textField{value: "Fix a login bug", limit: 2000}
	for _, hit := range m.dashboardLayout().hits {
		if hit.action != "dashboard-create" {
			continue
		}
		next, cmd := m.updateMouse(tea.MouseClickMsg{Button: tea.MouseLeft, X: m.contentX() + hit.x, Y: m.bodyStart() + hit.y})
		result := next.(model)
		if cmd != nil || result.screen != newPostScreen || result.form.fields[3].value != "Fix a login bug" {
			t.Fatal("create task must open a draft with the prompt, without publishing")
		}
		return
	}
	t.Fatal("create task button missing")
}

func TestDirectedTaskHidesAcceptanceForOtherWorkers(t *testing.T) {
	target := int64(2)
	m := model{screen: detailScreen, width: 80, height: 24, user: api.User{ID: "3"}, posts: []api.Post{
		{ID: 1, UserID: 1, TargetWorker: &target, Status: "open", EndTime: time.Now().Add(time.Hour)},
	}}
	for _, hit := range m.detailHeader().hits {
		if hit.action == "a" {
			t.Fatal("another seller's task must not offer acceptance")
		}
	}
	flow := m.taskFlow(m.posts[0], api.Escrow{}, "", api.Settlement{}, false)
	if !strings.Contains(flow.next, "Only the selected worker") {
		t.Fatal("missing directed-task explanation")
	}
}

func TestPendingFundingShowsRefreshInsteadOfFundingAgain(t *testing.T) {
	worker := int64(2)
	m := model{screen: detailScreen, width: 80, height: 24, user: api.User{ID: "1"}, escrow: api.Escrow{State: "pending"}, posts: []api.Post{
		{ID: 1, UserID: 1, AcceptedBy: &worker, Status: "negotiating"},
	}}
	found := false
	for _, hit := range m.detailHeader().hits {
		if hit.action == "f" {
			t.Fatal("pending funding must not offer another funding action")
		}
		if hit.action == "refresh" {
			found = true
		}
	}
	if !found {
		t.Fatal("pending funding needs a refresh action")
	}
}
