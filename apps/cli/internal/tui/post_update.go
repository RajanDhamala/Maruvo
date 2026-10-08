package tui

import (
	"errors"
	"net/http"

	tea "charm.land/bubbletea/v2"
	"github.com/rajandhamala/Maruvo/cli/internal/api"
)

func (m model) openPosts(own bool) (tea.Model, tea.Cmd) {
	m.stopWorkspace()
	m.own, m.loading, m.selected = own, true, 0
	m.posts, m.err, m.notice = nil, nil, ""
	m.deleting, m.editingStatus, m.scroll = false, false, 0
	m.recovering = ""
	m.profileOpen = false
	m.picker.open = false
	m.fundingConfirm = false
	m.escrow = api.Escrow{}
	m.dashboard.focus = dashboardTasks
	m.dashboard.filterOpen = false
	m.dashboard.generation++

	m.screen = feedScreen
	if own {
		m.screen = myPostsScreen
	}

	if m.dashboard.ready {
		m.applyDashboardPosts()
	}

	return m, m.fetchDashboard()
}

type sessionChecked struct {
	token string
	err   error
}

func (m *model) setPostError(err error) tea.Cmd {
	m.err = err

	var apiError *api.Error
	if errors.As(err, &apiError) && apiError.StatusCode == http.StatusUnauthorized {
		token, client, ctx := m.token, m.client, m.ctx

		return func() tea.Msg {
			_, checkErr := client.Me(ctx, token)
			return sessionChecked{token: token, err: checkErr}
		}
	}

	return nil
}

func (m model) updatePosts(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	if m.onDashboard() {
		return m.updateDashboard(msg)
	}

	if m.screen == workspaceScreen {
		return m.updateWorkspace(msg)
	}

	key := msg.String()
	if m.screen != newPostScreen && key == "q" {
		return m, tea.Quit
	}

	if m.loading {
		return m, nil
	}

	if m.screen == newPostScreen {
		return m.updateForm(msg)
	}

	if m.recovering != "" {
		switch key {
		case "enter", "y":
			m.loading, m.err = true, nil
			return m, m.recoverPost(m.posts[m.selected])
		case "d":
			if m.recovering == "reopen" {
				return m.openDeadline(), nil
			}
		case "t":
			if m.recovering == "reopen" && m.posts[m.selected].DeliverBy != nil {
				return m.openDeliveryDeadline(), nil
			}
		case "esc", "n":
			m.recovering = ""
		}

		return m, nil
	}

	if m.fundingConfirm {
		switch key {
		case "enter", "y":
			m.fundingConfirm, m.loading, m.err = false, true, nil
			return m, m.submitFunding()
		case "esc", "n":
			m.fundingConfirm = false
		}

		return m, nil
	}

	if m.deleting {
		switch key {
		case "y":
			m.loading, m.err = true, nil
			return m, m.deletePost(m.posts[m.selected])
		case "n", "esc":
			m.deleting = false
		}

		return m, nil
	}

	if m.editingStatus {
		switch key {
		case "up", "k":
			m.statusChoice = (m.statusChoice + len(statuses) - 1) % len(statuses)
		case "down", "j":
			m.statusChoice = (m.statusChoice + 1) % len(statuses)
		case "enter":
			m.loading, m.err = true, nil
			return m, m.updateStatus(m.posts[m.selected])
		case "esc":
			m.editingStatus = false
		}

		return m, nil
	}

	switch key {
	case "1":
		return m.openPosts(false)
	case "2":
		return m.openPosts(true)
	case "3", "n":
		m.stopWorkspace()
		m.screen, m.form = newPostScreen, newPostForm()
		m.err, m.notice = nil, ""
	case "l":
		return m.beginLogout()
	case "p":
		m.profileOpen = true
	case "w":
		m.loading, m.err = true, nil
		return m, m.connectWallet()
	case "r":
		m.loading, m.err, m.notice = true, nil, ""
		if m.screen == detailScreen && len(m.posts) > 0 {
			return m, m.postInfo()
		}

		return m, m.fetchDashboard()
	case "esc":
		if m.screen == detailScreen {
			return m.openPosts(m.own)
		}
	case "up", "k":
		if m.screen == detailScreen {
			m.scroll = max(0, m.scroll-1)
		} else {
			m.movePost(-1)
		}
	case "down", "j":
		if m.screen == detailScreen {
			m.scroll = min(m.detailScrollMax(), m.scroll+1)
		} else {
			m.movePost(1)
		}
	case "left", "[":
		if m.screen == feedScreen {
			m.level = (m.level + 2) % 3
			return m.openPosts(false)
		}

		if m.screen == myPostsScreen {
			return m.filterTasks((m.taskFilter + len(taskFilters) - 1) % len(taskFilters)), nil
		}
	case "right", "]":
		if m.screen == feedScreen {
			m.level = (m.level + 1) % 3
			return m.openPosts(false)
		}

		if m.screen == myPostsScreen {
			return m.filterTasks((m.taskFilter + 1) % len(taskFilters)), nil
		}
	case "enter":
		if len(m.listIndices()) != 0 {
			return m.openDetail()
		}
	case "a":
		if m.screen == detailScreen && len(m.posts) > 0 && !m.isPoster(m.posts[m.selected]) &&
			m.posts[m.selected].Status == "open" {
			m.loading, m.err = true, nil
			return m, m.acceptPost(m.posts[m.selected])
		}
	case "c", "m", "u":
		if m.screen == detailScreen && len(m.posts) > 0 && m.posts[m.selected].AcceptedBy != nil {
			action := ""
			if key != "c" {
				action = key
			}

			return m.openWorkspace(action)
		}
	case "f":
		if m.screen == detailScreen && len(m.posts) > 0 && m.isPoster(m.posts[m.selected]) &&
			m.posts[m.selected].AcceptedBy != nil &&
			m.posts[m.selected].Status == "negotiating" {
			m.loading, m.err = true, nil
			return m, m.prepareFunding()
		}
	case "s":
		if m.screen == detailScreen && len(m.posts) != 0 && m.isPoster(m.posts[m.selected]) &&
			m.posts[m.selected].AcceptedBy == nil {
			m.editingStatus, m.statusChoice = true, 0
			for i, status := range statuses {
				if status == m.posts[m.selected].Status {
					m.statusChoice = i
				}
			}
		}
	case "d":
		if m.screen == detailScreen && len(m.posts) != 0 && m.isPoster(m.posts[m.selected]) &&
			m.posts[m.selected].AcceptedBy == nil {
			m.deleting = true
		}
	case "x", "o":
		if m.screen == detailScreen && len(m.posts) != 0 {
			return m.beginRecovery(key)
		}
	}

	return m, nil
}

func unauthorized(err error) bool {
	var failure *api.Error
	return errors.As(err, &failure) && failure.StatusCode == http.StatusUnauthorized
}
