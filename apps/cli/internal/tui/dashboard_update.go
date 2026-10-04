package tui

import (
	"slices"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/rajandhamala/Maruvo/cli/internal/api"
)

func (m model) updateDashboard(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "ctrl+b":
		m.dashboard.filterOpen = false
		m.dashboard.sidebarHidden = !m.dashboard.sidebarHidden

		return m, nil
	case "ctrl+f":
		m.dashboard.filterOpen = false
		m.dashboard.focus = dashboardSearch

		return m, nil
	case "ctrl+t":
		m.dashboard.filterOpen = false
		m.dashboard.promptHidden = !m.dashboard.promptHidden

		m.dashboard.focus = dashboardTasks
		if !m.dashboard.promptHidden {
			m.dashboard.focus = dashboardPrompt
		}

		return m, nil
	}

	if m.loading {
		if m.dashboard.focus == dashboardTasks && msg.String() == "q" {
			return m, tea.Quit
		}

		return m, nil
	}

	key := msg.String()
	if m.dashboard.filterOpen {
		return m.updateDashboardFilter(msg)
	}

	if key == "tab" || key == "shift+tab" {
		m.dashboard.focus = (m.dashboard.focus + 1) % 2
		if m.dashboard.focus == dashboardPrompt {
			m.dashboard.promptHidden = false
		}

		return m, nil
	}

	if m.dashboard.focus == dashboardPrompt {
		switch key {
		case "esc":
			m.dashboard.focus = dashboardTasks
		case "enter":
			return m.createDashboardDraft(), nil
		default:
			m.homeInput.limit = 2000
			m.homeInput.key(msg)
		}

		return m, nil
	}

	if m.dashboard.focus == dashboardSearch {
		if key == "enter" || key == "esc" {
			m.dashboard.focus = dashboardTasks
		} else {
			m.dashboard.search.limit = 200
			m.dashboard.search.key(msg)
			m.selectVisiblePost()
		}

		return m, nil
	}

	switch key {
	case "q":
		return m, tea.Quit
	case "1":
		return m.switchDashboard(false)
	case "2":
		return m.switchDashboard(true)
	case "3", "n":
		return m.createDashboardDraft(), nil
	case "i":
		m.dashboard.promptHidden = false
		m.dashboard.focus = dashboardPrompt
	case "/":
		return m.openCommands(), nil
	case "f":
		return m.openDashboardFilter(), nil
	case "esc":
		m.dashboard.search = textField{}
		m.selectVisiblePost()
	case "up", "k":
		m.movePost(-1)
	case "down", "j":
		m.movePost(1)
	case "left", "[", "right", "]":
		direction := 1
		if key == "left" || key == "[" {
			direction = -1
		}

		if m.own {
			return m.filterTasks((m.taskFilter + direction + len(taskFilters)) % len(taskFilters)), nil
		}

		filter := m.level + 1
		if m.dashboard.all {
			filter = 0
		}

		return m.dashboardLevel((filter + direction + 4) % 4)
	case "enter":
		if len(m.listIndices()) != 0 {
			return m.openDetail()
		}

		return m.activateDashboardEmpty()
	case "a":
		for i, post := range m.dashboard.mine {
			if activeTask(post) {
				return m.openDashboardTask(i)
			}
		}

		m.notice = "No active work yet. Open a task to read its brief."
	case "r":
		m.loading, m.err, m.notice = true, nil, ""
		m.dashboard.generation++

		return m, m.fetchDashboard()
	case "w":
		m.loading, m.err = true, nil
		return m, m.connectWallet()
	case "p":
		m.profileOpen = true
	case "l":
		return m.beginLogout()
	}

	return m, nil
}

func (m model) switchDashboard(own bool) (tea.Model, tea.Cmd) {
	if !m.dashboard.ready {
		return m.openPosts(own)
	}

	m.own, m.selected, m.err, m.notice = own, 0, nil, ""

	m.screen = feedScreen
	if own {
		m.screen = myPostsScreen
	}

	m.dashboard.focus = dashboardTasks
	m.dashboard.filterOpen = false
	m.applyDashboardPosts()

	return m, nil
}

func (m model) dashboardLevel(filter int) (tea.Model, tea.Cmd) {
	m.dashboard.focus = dashboardTasks

	m.dashboard.all = filter == 0
	if filter > 0 {
		m.level = filter - 1
	}

	if !m.dashboard.ready {
		return m.openPosts(false)
	}

	m.selected = 0
	m.selectVisiblePost()

	return m, nil
}

func (m *model) applyDashboardPosts() {
	id := int64(0)
	if m.selected >= 0 && m.selected < len(m.posts) {
		id = m.posts[m.selected].ID
	}

	source := m.dashboard.feed
	if m.own {
		source = m.dashboard.mine
	}

	m.posts = slices.Clone(source)

	m.selected = 0
	for i, post := range m.posts {
		if post.ID == id {
			m.selected = i
			break
		}
	}

	m.selectVisiblePost()
}

func (m model) createDashboardDraft() model {
	m.dashboard.filterOpen = false
	m.screen, m.form = newPostScreen, newPostForm()
	m.err, m.notice = nil, ""

	draft := strings.TrimSpace(m.homeInput.value)
	if draft != "" {
		m.form.descriptionSource = descriptionFromText
		m.form.fields[3].insert(draft)
		title := []rune(strings.SplitN(draft, "\n", 2)[0])
		m.form.fields[0].insert(string(title[:min(80, len(title))]))
		m.form.focus = 1
	}

	return m
}

func (m model) openDashboardTask(index int) (tea.Model, tea.Cmd) {
	if index < 0 || index >= len(m.dashboard.mine) {
		return m, nil
	}

	m.posts = slices.Clone(m.dashboard.mine)
	m.own, m.selected, m.screen = true, index, myPostsScreen

	return m.openDetail()
}

func (m model) updateDashboardMouse(hit hitArea, x, y int) (tea.Model, tea.Cmd) {
	switch hit.action {
	case "dashboard-sidebar":
		return m.updateDashboard(tea.KeyPressMsg{Code: 'b', Mod: tea.ModCtrl})
	case "dashboard-scope":
		return m.switchDashboard(hit.index == 1)
	case "dashboard-level":
		return m.dashboardLevel(hit.index)
	case "dashboard-wallet":
		m.loading, m.err = true, nil
		return m, m.connectWallet()
	case "dashboard-task":
		return m.openDashboardTask(hit.index)
	case "dashboard-submit":
		return m.createDashboardDraft(), nil
	case "dashboard-search":
		m.dashboard.focus = dashboardSearch
		m.dashboard.search.cursor = len([]rune(m.dashboard.search.value))
	case "dashboard-filter":
		return m.openDashboardFilter(), nil
	case "dashboard-empty":
		return m.activateDashboardEmpty()
	case "dashboard-prompt":
		start := 0
		if m.dashboard.focus == dashboardPrompt {
			start = m.homeInput.visibleStart(hit.width - 5)
		}

		m.dashboard.focus = dashboardPrompt
		if y == hit.y+1 {
			m.homeInput.cursor = start
			column := max(0, x-hit.x-3)

			runes := []rune(m.homeInput.value)
			for m.homeInput.cursor < len(runes) {
				cells := ansi.StringWidth(string(runes[m.homeInput.cursor]))
				if column < cells {
					break
				}

				column -= cells
				m.homeInput.cursor++
			}
		}
	}

	return m, nil
}

func (m *model) clearDashboard() {
	m.dashboard = dashboardState{generation: m.dashboard.generation + 1}
}

func (m *model) updateDashboardPost(post api.Post) {
	for _, posts := range [][]api.Post{m.dashboard.feed, m.dashboard.mine} {
		for i := range posts {
			if posts[i].ID == post.ID {
				posts[i] = post
			}
		}
	}
}
