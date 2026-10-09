package tui

import (
	"fmt"
	"image/color"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

func (m model) View() tea.View {
	if m.invitations.pending != nil {
		return m.inviteView()
	}
	if m.workSetup.open {
		return m.workSetupView()
	}
	if m.permissions.open {
		return m.permissionsView()
	}
	if m.shortcutsOpen {
		return m.shortcutsView()
	}
	if m.localAgent.open && m.localAgent.approval != nil {
		return m.actionApprovalView()
	}

	if m.agentControls.open {
		return m.agentControlsView()
	}

	if m.providers.open {
		return m.providersView()
	}

	if m.remote.open {
		return m.remoteView()
	}

	if m.localAgentVisible() {
		return m.commandView(m.localAgentView())
	}

	if !m.demo {
		if m.token != "" {
			return m.commandView(m.postsView())
		}

		return m.commandView(m.authView())
	}

	s := fmt.Sprintf("Maruvo\n\nConnection demo\nAPI: %s\nMessage: %s\n\n", m.client.URL(), m.message)
	switch {
	case m.loggingIn:
		s += "Finish sign-in in your browser.\nWaiting for the login response...\n"
	case m.loading:
		s += "Sending to Rust through the Go API...\n"
	case m.err != nil:
		s += fmt.Sprintf("Connection failed: %v\nStart make rust and make api, then retry.\n", m.err)
	default:
		s += fmt.Sprintf("Connected to %s\n%s\n", m.response.Service, m.response.Message)
	}

	width, _ := m.dimensions()
	s += "\n" + compactHint("Enter / r send again · q quit", max(1, width-4)) + "\n"
	view := tea.NewView(s)
	view.AltScreen = true

	return view
}

func (m model) frame(body []string, footer string) tea.View {
	width, height := m.dimensions()
	if width < 48 || height < 16 {
		view := tea.NewView(
			"Maruvo\n\nResize the terminal to at least 48 columns × 16 rows.\nCtrl+c to quit.",
		)
		view.AltScreen = true

		return view
	}

	inner := width - 4
	header := accent("MARUVO")

	identity := "Task exchange"
	if m.profile != "" && m.profile != "default" {
		identity = "Profile: " + m.profile
	}

	if m.token != "" {
		identity = m.profileLabel()
	}

	identity = ansi.Truncate(identity, max(8, inner/2), "…")

	label := muted(identity)
	if m.profileOpen {
		label = accent(identity)
	}

	header += strings.Repeat(" ", max(1, inner-ansi.StringWidth(header)-ansi.StringWidth(identity))) + label
	tabs := muted("Welcome")

	if m.token != "" {
		names := append([]string(nil), tabNames...)

		active := int(m.screen)
		if m.screen == detailScreen || m.screen == workspaceScreen {
			active = 0
			if m.own {
				active = 1
			}
		}

		for i := range names {
			if i == active {
				names[i] = accent(names[i])
			} else {
				names[i] = muted(names[i])
			}
		}

		tabs = strings.Join(names, "    ")
	}

	divider := muted(strings.Repeat("─", inner))

	lines := []string{"", "  " + header, "", "  " + tabs, "  " + divider, ""}
	if m.screen == workspaceScreen {
		lines = []string{"", "  " + header, "  " + divider, ""}
	}

	for i := 0; i < m.bodyHeight(); i++ {
		line := ""
		if !m.picker.open && i < len(body) {
			line = ansi.Truncate(body[i], m.contentWidth(), "…")
		}

		lines = append(lines, strings.Repeat(" ", m.contentX())+line)
	}

	status := ""

	if m.screen != workspaceScreen && !m.picker.open && !m.profileOpen {
		if m.err != nil {
			status = warning(plain(m.err.Error()))
		} else if m.notice != "" {
			status = accent(plain(m.notice))
		} else if m.token != "" && !m.user.GitHubConnected && m.user.GitHubLogin == "" &&
			(m.screen == feedScreen || m.screen == myPostsScreen) {
			status = accent("Connect GitHub to your profile: Ctrl+g")
		}
	}

	if m.picker.open {
		footer = muted(
			compactHint("Arrows select · PgUp/PgDn month · Tab time · Enter apply · Esc cancel", inner),
		)
		if width < 78 {
			footer = muted(compactHint("Arrows · Tab time · Enter apply · Esc cancel", inner))
		}
	} else if m.profileOpen {
		footer = "w wallet · Enter log out · Esc close"
		if !m.user.GitHubConnected && m.user.GitHubLogin == "" {
			footer = "g connect GitHub · " + footer
		}

		footer = muted(compactHint(footer, inner))
	} else if m.err != nil && m.screen == workspaceScreen {
		footer = warning(plain(m.err.Error()))
	} else if m.notice != "" && m.screen == workspaceScreen {
		footer = accent(m.notice) + "   " + muted(compactHint(footer, inner))
	} else {
		footer = muted(compactHint(footer, inner))
	}

	statusLine := ""
	if status != "" {
		statusLine = "  " + ansi.Truncate(status, inner, "…")
	}

	lines = append(lines, statusLine, "  "+divider, "  "+ansi.Truncate(footer, inner, "…"), "")
	if m.profileOpen && m.token != "" {
		m.drawProfile(lines)
	}

	if m.picker.open && m.token != "" {
		m.drawDeadline(lines)
	}

	view := tea.NewView(strings.Join(lines, "\n"))
	view.AltScreen = true
	view.MouseMode = tea.MouseModeCellMotion
	view.BackgroundColor = color.RGBA{R: 10, G: 10, B: 10, A: 255}
	view.ForegroundColor = color.RGBA{R: 238, G: 238, B: 238, A: 255}

	return view
}

func (m model) dimensions() (int, int) {
	width, height := m.width, m.height
	if width == 0 {
		width = 80
	}

	if height == 0 {
		height = 24
	}

	return width, height
}

func plain(text string) string {
	return strings.Join(strings.Fields(ansi.Strip(text)), " ")
}

func bold(text string) string {
	return "\x1b[1m" + text + "\x1b[0m"
}

func accent(text string) string {
	return "\x1b[1;36m" + text + "\x1b[0m"
}

func muted(text string) string {
	return "\x1b[2m" + text + "\x1b[0m"
}

func warning(text string) string {
	return "\x1b[31m" + text + "\x1b[0m"
}
