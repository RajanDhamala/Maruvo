package tui

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/rajandhamala/Maruvo/cli/internal/api"
)

func TestCreationTimingsRequireDeliveryAndPreserveDraft(t *testing.T) {
	m := dashboardFixture()
	m.screen, m.form = newPostScreen, newPostForm()
	m.form.fields[0].insert("Explicit timing task")
	m.form.descriptionSource = descriptionFromText
	m.form.fields[3].insert("Keep this description.")

	if _, err := m.form.payload(); err == nil {
		t.Fatal("publishing must require an explicit delivery date")
	}

	acceptBy := m.form.fields[2].value
	next, _ := m.Update(tea.KeyPressMsg{Code: 'd', Mod: tea.ModCtrl})

	m = next.(model)
	if !m.form.timingOpen || m.form.timings[0].value != "24" || m.form.timings[2].value != "24" {
		t.Fatal("timing settings must expose editable defaults")
	}

	next, _ = m.Update(tea.KeyPressMsg{Code: 'u', Mod: tea.ModCtrl})
	next, _ = next.Update(tea.PasteMsg{Content: "8"})
	next, _ = next.Update(tea.KeyPressMsg{Code: tea.KeyTab})
	next, _ = next.Update(tea.KeyPressMsg{Code: tea.KeyEnter})

	m = next.(model)
	if !m.picker.open || !m.picker.delivery {
		t.Fatal("the delivery setting must open its own date picker")
	}

	next, _ = m.applyDeadline()
	next, _ = next.Update(tea.KeyPressMsg{Code: tea.KeyTab})
	next, _ = next.Update(tea.KeyPressMsg{Code: 'u', Mod: tea.ModCtrl})
	next, _ = next.Update(tea.PasteMsg{Content: "12"})
	next, _ = next.Update(tea.KeyPressMsg{Code: tea.KeyTab})
	next, _ = next.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = next.(model)

	payload, err := m.form.payload()
	if err != nil || m.form.timingOpen || payload.FundingWindowSeconds != 8*3600 ||
		payload.ReviewWindowSeconds != 12*3600 ||
		m.form.fields[2].value != acceptBy ||
		m.form.fields[3].value != "Keep this description." {
		t.Fatalf("timing edits must retain the original task draft and acceptance cutoff: %v", err)
	}

	deadline, _ := time.ParseInLocation("2006-01-02 15:04", acceptBy, time.Local)

	m.form.timings[1].value = deadline.Add(8 * time.Hour).Format("2006-01-02 15:04")
	if _, err := m.form.payload(); err == nil {
		t.Fatal("delivery must follow the entire acceptance and funding window")
	}
}

func TestTimingControlsFitAndSupportMouse(t *testing.T) {
	for _, size := range [][2]int{{48, 16}, {80, 24}, {120, 36}} {
		m := dashboardFixture()
		m.width, m.height, m.screen, m.form = size[0], size[1], newPostScreen, newPostForm()
		m.form.focus = 8
		found := false

		for _, hit := range m.formLayout().hits {
			if hit.action != "timings" || hit.height == 0 {
				continue
			}

			next, _ := m.updateMouse(
				tea.MouseClickMsg{X: m.contentX() + hit.x, Y: m.bodyStart() + hit.y, Button: tea.MouseLeft},
			)
			m, found = next.(model), true

			break
		}

		if !found || !m.form.timingOpen {
			t.Fatalf("%v: timings must be reachable by mouse", size)
		}

		layout := m.formLayout()
		if len(layout.rows) > m.bodyHeight() {
			t.Fatalf("%v: timing controls exceed body height", size)
		}

		for _, row := range layout.rows {
			if ansi.StringWidth(row) > m.contentWidth() {
				t.Fatalf("%v: timing row overflow", size)
			}
		}

		for _, hit := range layout.hits {
			if hit.action != "timing-field" || hit.index != 1 {
				continue
			}

			next, _ := m.updateMouse(
				tea.MouseClickMsg{X: m.contentX() + hit.x, Y: m.bodyStart() + hit.y, Button: tea.MouseLeft},
			)
			if !next.(model).picker.delivery || !next.(model).picker.open {
				t.Fatal("delivery must have a working mouse target")
			}
		}

		m = recoveryFixture()
		m.width, m.height = size[0], size[1]
		delivery := time.Now().Add(72 * time.Hour)
		m.posts[0].DeliverBy, m.posts[0].FundingWindowSeconds, m.posts[0].ReviewWindowSeconds = &delivery, 86400, 86400
		m.escrow.State = "prepared"
		next, _ := m.beginRecovery("o")
		m = next.(model)

		layout = m.recoveryLayout()
		if len(layout.rows) > m.bodyHeight() {
			t.Fatalf("%v: recovery date controls exceed body height", size)
		}

		for _, hit := range layout.hits {
			if hit.y+hit.height > m.bodyHeight() {
				t.Fatalf("%v: recovery controls must remain reachable", size)
			}
		}

		m.fundingConfirm, m.recovering = true, ""
		fundBy := time.Now().Add(24 * time.Hour)
		m.posts[0].FundBy = &fundBy

		layout = m.detailLayout()
		if len(layout.rows) > m.bodyHeight() ||
			!strings.Contains(strings.Join(layout.rows, "\n"), "Review 24h") && size[1] == 16 {
			t.Fatalf("%v: funding approval must show timing terms and signing controls", size)
		}

		m.fundingConfirm = false
		overdue := time.Now().Add(-time.Hour)
		m.posts[0].Deadline = api.TaskDeadline{Stage: "fund", DueAt: &overdue, Overdue: true}
		layout = m.detailLayout()

		if !strings.Contains(strings.Join(layout.rows, "\n"), "Funding overdue") {
			t.Fatalf("%v: overdue status must remain visible above the scrolling brief", size)
		}
	}
}

func TestLiveDeadlineReviewWindowsAndOverdueStatus(t *testing.T) {
	m := reviewerModel()
	delivery, reviewBy := time.Now().Add(-time.Hour), time.Now().Add(time.Hour)
	m.workspace.Post.DeliverBy, m.workspace.Post.FundingWindowSeconds, m.workspace.Post.ReviewWindowSeconds = &delivery, 86400, 3600
	submitted := time.Now()
	data, _ := json.Marshal(
		map[string]any{
			"submission_version": 3,
			"submitted_at":       submitted,
			"review_by":          reviewBy,
			"note":               "Result",
		},
	)
	m.applyWorkspaceEvent(api.WorkspaceEvent{PostID: 1, ID: 1, Kind: "work.submitted", Data: data})

	if m.workspace.State.ReviewBy == nil || !m.workspace.State.ReviewBy.Equal(reviewBy) ||
		m.workspace.Post.Deadline.Stage != "review" {
		t.Fatal("live submission must start the supplied review deadline")
	}

	data, _ = json.Marshal(map[string]any{"stage": "review", "submission_version": 2, "due_at": delivery})
	m.applyWorkspaceEvent(api.WorkspaceEvent{PostID: 1, ID: 2, Kind: "task.overdue", Data: data})

	if !m.workspace.Post.Deadline.DueAt.Equal(reviewBy) {
		t.Fatal("stale review notices must not replace the latest window")
	}

	m.applyWorkspaceEvent(
		api.WorkspaceEvent{
			PostID: 1,
			ID:     3,
			Kind:   "review.changes_requested",
			Data:   json.RawMessage(`{"note":"Revise"}`),
		},
	)

	if m.workspace.State.ReviewBy != nil || m.workspace.Post.DeliverBy != &delivery ||
		m.workspace.Post.Deadline.Stage != "deliver" ||
		deadlineLabel(m.workspace.Post.Deadline) != "Delivery overdue" {
		t.Fatal("requested changes must preserve delivery terms and close review timing")
	}

	m.applyWorkspaceEvent(
		api.WorkspaceEvent{
			PostID: 1,
			ID:     4,
			Kind:   "review.completed",
			Data:   json.RawMessage(`{"review_state":"approved"}`),
		},
	)

	if deadlineLabel(m.workspace.Post.Deadline) != "" {
		t.Fatal("completed reviews must stop active overdue indicators")
	}
}
