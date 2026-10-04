package tui

import (
	"errors"
	"slices"
	"testing"

	tea "charm.land/bubbletea/v2"
)

func TestDashboardDropdownKeyboard(t *testing.T) {
	m := dashboardFixture()
	m.homeInput = textField{value: "Retain this draft", cursor: 17}
	m.selected = 2

	next, _ := m.Update(tea.KeyPressMsg{Code: 'f', Text: "f"})
	next, _ = next.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	next, _ = next.Update(tea.PasteMsg{Content: "Ignore menu paste"})

	m = next.(model)
	if !m.dashboard.filterOpen || !m.dashboard.all || m.selected != 2 ||
		m.homeInput.value != "Retain this draft" {
		t.Fatal("moving in the dropdown must not apply filters, change selection, or edit the prompt")
	}

	next, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEsc})

	m = next.(model)
	if m.dashboard.filterOpen || !m.dashboard.all || m.selected != 2 {
		t.Fatal("Esc must dismiss the menu and retain the applied filter and selection")
	}

	next, _ = m.Update(tea.KeyPressMsg{Code: 'f', Text: "f"})
	next, _ = next.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	next, _ = next.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	next, cmd := next.Update(tea.KeyPressMsg{Code: tea.KeyEnter})

	m = next.(model)
	if cmd != nil || m.dashboard.filterOpen || m.dashboard.all || m.level != 1 ||
		!slices.Equal(m.listIndices(), []int{1}) || m.selected != 1 {
		t.Fatal("Enter must apply the chosen level from the snapshot and select the original task index")
	}

	next, _ = m.Update(tea.KeyPressMsg{Code: '2', Text: "2"})
	next, _ = next.Update(tea.KeyPressMsg{Code: 'f', Text: "f"})
	next, _ = next.Update(tea.KeyPressMsg{Code: tea.KeyUp})
	next, cmd = next.Update(tea.KeyPressMsg{Code: tea.KeyEnter})

	m = next.(model)
	if cmd != nil || m.taskFilter != 2 || !slices.Equal(m.listIndices(), []int{0}) ||
		m.posts[m.selected].ID != 40 {
		t.Fatal("Personal's dropdown must filter by the user's actual role")
	}
}

func TestDashboardDropdownMouse(t *testing.T) {
	m := dashboardFixture().openDashboardFilter()
	area := m.dashboardFilterArea()
	click := tea.MouseClickMsg{X: m.contentX() + area.x + 2,
		Y: m.bodyStart() + area.y + 3, Button: tea.MouseLeft}

	next, cmd := m.Update(click)

	m = next.(model)
	if cmd != nil || m.dashboard.filterOpen || m.level != 1 || m.selected != 1 || !m.onDashboard() {
		t.Fatal("clicking Medium must apply the filter without opening a task behind the menu")
	}

	m = m.openDashboardFilter()
	next, _ = m.Update(tea.MouseWheelMsg{X: click.X, Y: click.Y, Button: tea.MouseWheelDown})

	m = next.(model)
	if m.dashboard.filterChoice != 3 || m.level != 1 || m.selected != 1 {
		t.Fatal("wheel input inside the dropdown should move its choice without changing the feed")
	}

	next, _ = m.Update(tea.MouseWheelMsg{X: m.contentX(), Y: m.bodyStart() + 2, Button: tea.MouseWheelDown})
	if next.(model).selected != 1 || next.(model).dashboard.filterChoice != 3 {
		t.Fatal("wheel input outside the dropdown should not change the menu or underlying selection")
	}

	next, cmd = m.Update(tea.MouseClickMsg{X: m.contentX(), Y: m.bodyStart() + 2, Button: tea.MouseLeft})

	m = next.(model)
	if cmd != nil || m.dashboard.filterOpen || !m.onDashboard() || m.level != 1 {
		t.Fatal("an outside click must only dismiss the dropdown")
	}
}

func TestDashboardEmptyActions(t *testing.T) {
	for _, action := range []string{"create", "search", "role", "level", "retry"} {
		t.Run(action, func(t *testing.T) {
			m := dashboardFixture()

			switch action {
			case "create":
				m.posts, m.dashboard.feed, m.dashboard.mine = nil, nil, nil
				m.homeInput = textField{value: "A saved task brief", cursor: 18}
			case "search":
				m.dashboard.search = textField{value: "no-such-task", cursor: 12}
			case "role":
				m.own, m.screen, m.taskFilter = true, myPostsScreen, 2
				m.dashboard.mine = m.dashboard.mine[1:]
				m.applyDashboardPosts()
			case "level":
				m.dashboard.all, m.level = false, 2
				m.dashboard.feed = m.dashboard.feed[:2]
				m.applyDashboardPosts()
			case "retry":
				m.posts, m.dashboard.ready = nil, false
				m.err = errors.New("connection failed")
			}

			if len(m.listIndices()) != 0 {
				t.Fatal("fixture must start with an empty visible list")
			}

			next, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
			after := next.(model)

			switch action {
			case "create":
				if cmd != nil || after.screen != newPostScreen ||
					after.form.fields[3].value != "A saved task brief" {
					t.Fatal("empty feed Enter should prepare the saved draft without publishing")
				}
			case "retry":
				if cmd == nil || !after.loading || after.err != nil || !after.onDashboard() {
					t.Fatal("failed initial load should retry the snapshot request")
				}
			default:
				if cmd != nil || !after.onDashboard() || len(after.listIndices()) == 0 {
					t.Fatal("filtered empty state should reveal real tasks without opening a form")
				}
			}

			found := false

			for _, hit := range m.dashboardLayout().hits {
				if hit.action != "dashboard-empty" {
					continue
				}

				found = true

				if action == "create" && (hit.x < m.dashboardGeometry().mainWidth/3 ||
					hit.y < m.dashboardGeometry().listHeight/3) {
					t.Fatal("empty-state action should sit in the center of the list region")
				}

				next, mouseCmd := m.Update(tea.MouseClickMsg{X: m.contentX() + hit.x,
					Y: m.bodyStart() + hit.y, Button: tea.MouseLeft})

				clicked := next.(model)
				if clicked.screen != after.screen || clicked.selected != after.selected ||
					clicked.dashboard.search.value != after.dashboard.search.value ||
					(mouseCmd == nil) != (cmd == nil) {
					t.Fatal("mouse and Enter should activate the same empty-state action")
				}
			}

			if !found {
				t.Fatal("empty state must have an actionable mouse target")
			}
		})
	}
}
