package tui

import (
	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

func (m model) updateMouse(msg tea.MouseClickMsg) (tea.Model, tea.Cmd) {
	width, height := m.dimensions()
	if msg.Button != tea.MouseLeft || m.demo || m.token == "" || m.loading || width < 48 || height < 16 {
		return m, nil
	}

	if m.picker.open {
		return m.updateDeadlineMouse(msg)
	}

	profile := m.profileArea()
	if msg.Y == 1 && msg.X >= profile.x && msg.X < profile.x+profile.width {
		m.profileOpen = !m.profileOpen
		return m, nil
	}

	if m.profileOpen {
		menu := m.profileMenuArea()
		if msg.X >= menu.x && msg.X < menu.x+menu.width && msg.Y >= menu.y && msg.Y < menu.y+menu.height {
			if msg.Y == menu.y+2 && msg.X > menu.x && msg.X < menu.x+menu.width-1 {
				m.profileOpen, m.loading, m.err = false, true, nil
				return m, m.connectWallet()
			}

			if msg.Y == menu.y+3 && msg.X > menu.x && msg.X < menu.x+menu.width-1 {
				return m.beginLogout()
			}
		} else {
			m.profileOpen = false
		}

		return m, nil
	}

	if msg.Y == 3 && m.screen != workspaceScreen {
		x := 2
		for i, name := range tabNames {
			if msg.X >= x && msg.X < x+len(name) {
				switch i {
				case 0:
					return m.openPosts(false)
				case 1:
					return m.openPosts(true)
				case 2:
					m.stopWorkspace()

					if m.screen != newPostScreen {
						m.screen, m.form = newPostScreen, newPostForm()
						m.deleting, m.editingStatus = false, false
						m.err, m.notice = nil, ""
					}

					return m, nil
				}
			}

			x += len(name) + 4
		}
	}

	x, y := msg.X-m.contentX(), msg.Y-m.bodyStart()
	if y < 0 || y >= m.bodyHeight() {
		return m, nil
	}

	for _, hit := range m.postsLayout().hits {
		if x < hit.x || x >= hit.x+hit.width || y < hit.y || y >= hit.y+hit.height {
			continue
		}

		switch hit.action {
		case "refresh":
			return m.updatePosts(tea.KeyPressMsg{Code: 'r', Text: "r"})
		case "field":
			if hit.index == 2 {
				return m.openDeadline(), nil
			}

			field := &m.form.fields[hit.index]

			start := 0
			if m.form.focus == hit.index {
				start = field.visibleStart(hit.width - 2)
			}

			m.form.focus = hit.index
			if y == hit.y+1 {
				field.cursor = start
				runes := []rune(field.value)
				column := max(0, x-hit.x-1)

				for field.cursor < len(runes) {
					cells := ansi.StringWidth(string(runes[field.cursor]))
					if column < cells {
						break
					}

					column -= cells
					field.cursor++
				}
			}
		case "level":
			m.form.focus, m.form.level = 7, hit.index
		case "feed-level":
			if m.level != hit.index {
				m.level = hit.index
				return m.openPosts(false)
			}
		case "post":
			m.selected = hit.index
			return m.openDetail()
		case "publish":
			return m.submitPost()
		case "new":
			return m.updatePosts(tea.KeyPressMsg{Code: 'n', Text: "n"})
		case "back":
			if m.screen == newPostScreen {
				return m.openPosts(m.own)
			}

			return m.updatePosts(tea.KeyPressMsg{Code: tea.KeyEsc})
		case "s", "d", "a", "f", "c", "m", "u", "b", "v", "x", "e":
			if m.screen == workspaceScreen && m.workspaceAction == "" && !m.reviewConfirm {
				return m.workspaceCommand(hit.action)
			}

			return m.updatePosts(tea.KeyPressMsg{Code: rune(hit.action[0]), Text: hit.action})
		case "workspace-tab":
			return m.workspaceCommand("tab")
		case "workspace-file":
			m.fileSelection = hit.index
		case "workspace-download":
			return m.downloadWorkspaceFile(hit.index)
		case "local-file":
			m.composer.selection = hit.index
			return m.attachSuggestion()
		case "remove-attachment":
			m.composer.attachments = append(
				m.composer.attachments[:hit.index],
				m.composer.attachments[hit.index+1:]...)
		case "workspace-send":
			return m.updatePosts(tea.KeyPressMsg{Code: tea.KeyEnter})
		case "status":
			m.statusChoice = hit.index
		case "save-status":
			return m.updatePosts(tea.KeyPressMsg{Code: tea.KeyEnter})
		case "fund-confirm":
			return m.updatePosts(tea.KeyPressMsg{Code: tea.KeyEnter})
		case "delete":
			return m.updatePosts(tea.KeyPressMsg{Code: 'y', Text: "y"})
		case "cancel":
			return m.updatePosts(tea.KeyPressMsg{Code: tea.KeyEsc})
		}

		return m, nil
	}

	return m, nil
}

func (m model) updateWheel(msg tea.MouseWheelMsg) (tea.Model, tea.Cmd) {
	width, height := m.dimensions()
	if m.demo || m.token == "" || m.loading || m.profileOpen || width < 48 || height < 16 {
		return m, nil
	}

	code := tea.KeyDown

	switch msg.Button {
	case tea.MouseWheelUp:
		code = tea.KeyUp
	case tea.MouseWheelDown:
	default:
		return m, nil
	}

	if m.picker.open {
		area := m.deadlineArea()
		if msg.X < area.x || msg.X >= area.x+area.width || msg.Y < area.y || msg.Y >= area.y+area.height {
			return m, nil
		}

		return m.updateDeadline(tea.KeyPressMsg{Code: code})
	}

	if msg.Y < m.bodyStart() || msg.Y >= height-4 {
		return m, nil
	}

	if m.screen == workspaceScreen && m.composing() {
		if code == tea.KeyUp {
			m.activityScroll += 3
		} else {
			m.activityScroll = max(0, m.activityScroll-3)
		}

		return m, nil
	}

	return m.updatePosts(tea.KeyPressMsg{Code: code})
}
