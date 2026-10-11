package tui

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

func (m model) dashboardFilterOptions() []string {
	if m.own {
		return []string{"All roles", "Created", "Accepted"}
	}

	return []string{"All levels", "Easy", "Medium", "Complex"}
}

func (m model) dashboardFilterIndex() int {
	if m.own {
		return m.taskFilter
	}

	if m.dashboard.all {
		return 0
	}

	return m.level + 1
}

func (m model) dashboardToolbar(l *postLayout, width int) (string, string) {
	left := dashboardChoice("Feed", !m.own) + "  " + dashboardChoice("Personal", m.own)

	l.hit(0, 0, 4, 1, "dashboard-scope", 0)
	l.hit(6, 0, 8, 1, "dashboard-scope", 1)

	count := len(m.listIndices())
	if count > 0 && width >= 64 {
		left += muted(fmt.Sprintf("  %d tasks", count))
	}

	filter := m.dashboardFilterOptions()[m.dashboardFilterIndex()] + " ▾"
	search := "\uf002 Search"
	x := width - ansi.StringWidth(filter) - 3 - ansi.StringWidth(search)
	l.hit(x, 0, ansi.StringWidth(filter), 1, "dashboard-filter", 0)
	l.hit(width-ansi.StringWidth(search), 0, ansi.StringWidth(search), 1, "dashboard-search", 0)

	filter = dashboardChoice(filter, m.dashboard.filterOpen || m.dashboardFilterIndex() != 0)
	search = dashboardChoice(search, m.dashboard.focus == dashboardSearch || m.dashboard.search.value != "")
	right := filter + "   " + search
	left = ansi.Truncate(left, width-ansi.StringWidth(right)-2, "…")

	return left, right
}

func (m model) dashboardFilterArea() hitArea {
	width := m.dashboardGeometry().mainWidth
	filter := m.dashboardFilterOptions()[m.dashboardFilterIndex()] + " ▾"
	x := width - ansi.StringWidth(filter) - 3 - ansi.StringWidth("\uf002 Search")

	return hitArea{x: min(x, width-22), y: 1, width: 22, height: len(m.dashboardFilterOptions()) + 2}
}

func (m model) drawDashboardFilter(l *postLayout) {
	area := m.dashboardFilterArea()
	rows := []string{muted("╭" + strings.Repeat("─", area.width-2) + "╮")}

	for i, label := range m.dashboardFilterOptions() {
		mark := "  "
		if i == m.dashboardFilterIndex() {
			mark = "✓ "
		}

		text := " " + mark + label

		text += strings.Repeat(" ", area.width-2-ansi.StringWidth(text))
		if i == m.dashboard.filterChoice {
			text = "\x1b[1;30;46m" + text + "\x1b[0m"
		}

		rows = append(rows, muted("│")+text+muted("│"))
	}

	rows = append(rows, muted("╰"+strings.Repeat("─", area.width-2)+"╯"))
	drawOverlay(m.contentWidth(), l.rows, area, rows)
}

func (m model) openDashboardFilter() model {
	m.dashboard.focus = dashboardTasks
	m.dashboard.filterChoice = m.dashboardFilterIndex()
	m.dashboard.filterOpen = true

	return m
}

func (m model) updateDashboardFilter(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	count := len(m.dashboardFilterOptions())

	switch msg.String() {
	case "up", "k":
		m.dashboard.filterChoice = (m.dashboard.filterChoice + count - 1) % count
	case "down", "j":
		m.dashboard.filterChoice = (m.dashboard.filterChoice + 1) % count
	case "enter":
		return m.selectDashboardFilter(m.dashboard.filterChoice)
	case "esc", "q", "f":
		m.dashboard.filterOpen = false
	case "tab", "shift+tab":
		m.dashboard.filterOpen = false
		m.dashboard.promptHidden = false
		m.dashboard.focus = dashboardPrompt
	}

	return m, nil
}

func (m model) selectDashboardFilter(index int) (tea.Model, tea.Cmd) {
	if index < 0 || index >= len(m.dashboardFilterOptions()) {
		return m, nil
	}

	m.dashboard.filterOpen = false

	m.dashboard.focus = dashboardTasks
	if m.own {
		return m.filterTasks(index), nil
	}

	return m.dashboardLevel(index)
}

func (m model) updateDashboardFilterMouse(msg tea.MouseClickMsg) (tea.Model, tea.Cmd) {
	area := m.dashboardFilterArea()

	x, y := msg.X-m.contentX(), msg.Y-m.bodyStart()
	if x >= area.x && x < area.x+area.width && y >= area.y && y < area.y+area.height {
		if y > area.y && y < area.y+area.height-1 {
			return m.selectDashboardFilter(y - area.y - 1)
		}

		return m, nil
	}

	m.dashboard.filterOpen = false

	return m, nil
}

type dashboardEmptyState struct {
	title, description, label, action string
}

func (m model) dashboardEmptyState() dashboardEmptyState {
	switch {
	case m.loading:
		return dashboardEmptyState{title: "Loading your tasks…"}
	case !m.dashboard.ready && m.err != nil:
		return dashboardEmptyState{
			"Couldn't load your tasks.", "Check the connection and try again.", " Retry ", "retry",
		}
	case strings.TrimSpace(m.dashboard.search.value) != "":
		return dashboardEmptyState{
			"No matching tasks.", "Try another search or clear it.", " Clear search ", "search",
		}
	case m.own && m.taskFilter != 0 && len(m.dashboard.mine) > 0:
		return dashboardEmptyState{
			"No tasks in this role.", "Explore the rest of your tasks.", " Show all tasks ", "role",
		}
	case !m.own && !m.dashboard.all && len(m.dashboard.feed) > 0:
		return dashboardEmptyState{
			"No tasks at this level.", "Try a different level of work.", " All levels ", "level",
		}
	case m.own:
		return dashboardEmptyState{
			"Your tasks will appear here.",
			"Keep track of work you create or accept.",
			" + Create task ",
			"create",
		}
	default:
		return dashboardEmptyState{
			"No open tasks yet.",
			"Create a task. Fund escrow after a worker accepts.",
			" + Create task ",
			"create",
		}
	}
}

func (m model) dashboardEmpty(l *postLayout, width, start, height int) {
	state := m.dashboardEmptyState()

	rows := 1
	if state.label != "" {
		rows = 2
	}

	if height-start >= 5 && state.description != "" {
		rows = 4
	}

	y := start + max(0, (height-start-rows)/2)
	put := func(row int, text string) {
		text = ansi.Truncate(text, width, "…")
		x := max(0, (width-ansi.StringWidth(text))/2)
		l.rows[row] = strings.Repeat(" ", x) + text
	}
	put(y, bold(state.title))

	if rows == 4 {
		put(y+1, muted(state.description))
	}

	if state.label != "" {
		text := button(
			state.label,
			m.dashboard.focus == dashboardTasks && !m.dashboard.filterOpen && !m.profileOpen,
		)
		put(y+rows-1, text)
		l.hit((width-ansi.StringWidth(text))/2, y+rows-1, ansi.StringWidth(text), 1, "dashboard-empty", 0)
	}
}

func (m model) activateDashboardEmpty() (tea.Model, tea.Cmd) {
	m.dashboard.focus = dashboardTasks

	m.dashboard.filterOpen = false
	switch m.dashboardEmptyState().action {
	case "create":
		return m.createDashboardDraft(), nil
	case "search":
		m.dashboard.search = textField{}
		m.selectVisiblePost()
	case "role":
		return m.filterTasks(0), nil
	case "level":
		return m.dashboardLevel(0)
	case "retry":
		return m.updateDashboard(tea.KeyPressMsg{Code: 'r', Text: "r"})
	}

	return m, nil
}
