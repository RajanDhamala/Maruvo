package tui

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/rajandhamala/Maruvo/cli/internal/api"
)

func deliveryReviewFixture() model {
	m := dashboardFixture()
	m.screen, m.workspaceReview = workspaceScreen, true
	m.workspace = reviewerModel().workspace
	m.workspace.Post.Title = "Build a Solana transaction receipt CLI"
	m.workspace.Escrow.Network = "devnet"
	submitted, due := time.Now().Add(-time.Hour), time.Now().Add(23*time.Hour)
	m.workspace.State.SubmittedAt, m.workspace.State.ReviewBy = &submitted, &due
	m.workspace.State.Submission = "## Summary\n\nSource code and documentation.\n\n## Verification\n\nWorker ran go test ./...: PASS."
	m.workspace.Files = []api.WorkspaceFile{
		{ID: "input", Name: "private-input.txt", Purpose: "input"},
		{ID: "old-output", Name: "previous-output.txt", Purpose: "output"},
		{ID: "source", Name: "task6-solana-tx-receipt.tar.gz", Size: 9216, SHA256: strings.Repeat("a", 64)},
		{ID: "readme", Name: "README.md", Size: 1200, SHA256: strings.Repeat("b", 64)},
	}
	m.workspace.State.DeliveryFiles = []string{"source", "readme"}
	m.composer.root = "/tmp/maruvo-review"
	return m
}

func TestDeliveryReviewPagingKeepsControlsVisible(t *testing.T) {
	for _, size := range [][2]int{{48, 16}, {60, 20}, {80, 24}, {120, 36}, {168, 48}} {
		m := deliveryReviewFixture()
		m.width, m.height = size[0], size[1]
		m.workspace.State.Submission += "\n\n" + strings.Repeat("Long delivery report with concrete implementation and test details.\n", 80)
		m.workspace.State.ReviewNote = "Final requested revision."
		for _, key := range []rune{tea.KeyPgDown, tea.KeyEnd, tea.KeyPgUp, tea.KeyHome} {
			next, cmd := m.updateWorkspace(tea.KeyPressMsg{Code: key})
			m = next.(model)
			if cmd != nil || m.workspaceAction != "" || m.reviewConfirm || m.loading {
				t.Fatal("scrolling review must not download, prepare or sign a payment")
			}
			l := m.postsLayout()
			if len(l.rows) > m.bodyHeight() {
				t.Fatalf("%v: delivery review exceeds the viewport", size)
			}
			controls := map[string]bool{"a": false, "e": false, "x": false, "workspace-tab": false}
			for _, hit := range l.hits {
				if hit.x < 0 || hit.y < 0 || hit.x+hit.width > m.contentWidth() || hit.y+hit.height > m.bodyHeight() {
					t.Fatalf("%v: invisible review control: %+v", size, hit)
				}
				if _, ok := controls[hit.action]; ok {
					controls[hit.action] = true
				}
			}
			for action, visible := range controls {
				if !visible {
					t.Fatalf("%v: paging hid review action %s", size, action)
				}
			}
			view := m.View().Content
			rows := strings.Split(view, "\n")
			if len(rows) != size[1] {
				t.Fatalf("%v: review frame exceeds terminal height", size)
			}
			for _, row := range rows {
				if ansi.StringWidth(row) > size[0] {
					t.Fatalf("%v: review frame exceeds terminal width", size)
				}
			}
			if key == tea.KeyEnd && !strings.Contains(ansi.Strip(view), "Final requested revision.") {
				t.Fatalf("%v: End did not reveal the final review note", size)
			}
		}
		if m.activityScroll != 0 {
			t.Fatalf("%v: Home did not return to the delivery summary", size)
		}
		for _, event := range []tea.Msg{
			tea.KeyPressMsg{Code: tea.KeyDown},
			tea.KeyPressMsg{Code: tea.KeyUp},
			tea.MouseWheelMsg{Button: tea.MouseWheelDown, X: m.contentX(), Y: m.bodyStart()},
			tea.MouseWheelMsg{Button: tea.MouseWheelUp, X: m.contentX(), Y: m.bodyStart()},
		} {
			next, cmd := m.Update(event)
			m = next.(model)
			if cmd != nil {
				t.Fatal("review scrolling returned a mutation command")
			}
			if (event == (tea.KeyPressMsg{Code: tea.KeyDown}) || event == (tea.MouseWheelMsg{Button: tea.MouseWheelDown, X: m.contentX(), Y: m.bodyStart()})) && m.activityScroll == 0 {
				t.Fatal("down must move forward through the delivery report")
			}
		}
		if m.activityScroll != 0 {
			t.Fatal("up must return to the delivery summary")
		}
	}
}

func TestDeliveryTreeUsesExactSubmittedFiles(t *testing.T) {
	m := deliveryReviewFixture()
	m.width, m.height = 120, 36
	m.workspace.State.DeliveryFiles = []string{"readme", "source", "missing-file-id"}
	l := m.postsLayout()
	view := ansi.Strip(strings.Join(l.rows, "\n"))
	for _, term := range []string{"delivery-v2/", "README.md", "task6-solana-tx-receipt.tar.gz", "9.0 KiB", "SHA256", "Unavailable file", "Worker report"} {
		if !strings.Contains(view, term) {
			t.Fatalf("delivery view is missing %s", term)
		}
	}
	for _, excluded := range []string{"private-input.txt", "previous-output.txt"} {
		if strings.Contains(view, excluded) {
			t.Fatalf("delivery tree incorrectly included %s", excluded)
		}
	}
	downloads := 0
	for _, hit := range l.hits {
		if hit.action != "workspace-download" {
			continue
		}
		downloads++
		file := m.workspace.Files[hit.index]
		if file.ID != "readme" && file.ID != "source" {
			t.Fatal("delivery download points at an unsubmitted file")
		}
		next, cmd := m.updateMouse(tea.MouseClickMsg{Button: tea.MouseLeft, X: m.contentX() + hit.x, Y: m.bodyStart() + hit.y})
		result := next.(model)
		if cmd != nil || result.workspaceAction != "d" || result.fileSelection != hit.index ||
			result.workspaceInput.value != filepath.Join(m.composer.root, file.Name) {
			t.Fatal("file click must open the correct download destination without writing a file")
		}
	}
	if downloads != 2 {
		t.Fatal("missing file should have no download action")
	}
	// Previously visible file rows must lose their mouse targets when paged away.
	m.workspace.State.Submission += strings.Repeat("\nLong report", 80)
	m.activityScroll = m.reviewMaxScroll()
	for _, hit := range m.postsLayout().hits {
		if hit.action == "workspace-download" {
			t.Fatal("a scrolled-away file still receives clicks")
		}
	}
}

func TestDeliveryReviewPreservesParagraphsAndReportedEvidence(t *testing.T) {
	m := deliveryReviewFixture()
	body := m.reviewContent()
	view := ansi.Strip(strings.Join(body.rows, "\n"))
	if !strings.Contains(view, "Summary\n\nSource code") || !strings.Contains(view, "worker-reported tests") ||
		!strings.Contains(view, "Worker ran go test ./...: PASS.") {
		t.Fatal("delivery report lost its paragraph structure or evidence attribution")
	}
	code := ansi.Strip(strings.Join(reviewTextRows("```go\nfunc main() {\n    run()\n}\n```", 44), "\n"))
	if !strings.Contains(code, "\n    run()\n") {
		t.Fatal("code blocks lost their indentation")
	}
	m.workspace.CanReview = false
	for _, hit := range m.reviewButtons().hits {
		if hit.action == "a" || hit.action == "e" || hit.action == "x" {
			t.Fatal("a contributor must not receive reviewer actions")
		}
	}
	m.workspace.CanReview, m.workspace.Escrow.State = true, "unfunded"
	for _, hit := range m.reviewButtons().hits {
		if hit.action == "a" || hit.action == "e" || hit.action == "x" {
			t.Fatal("an unfunded delivery must not expose settlement actions")
		}
	}
}
