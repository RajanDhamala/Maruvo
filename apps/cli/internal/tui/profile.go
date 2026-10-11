package tui

import (
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

func (m model) profileLabel() string {
	width, _ := m.dimensions()
	if m.onDashboard() {
		width = m.contentWidth() + 4
		if sidebarWidth := m.dashboardGeometry().sideWidth; sidebarWidth > 0 {
			width = sidebarWidth * 2
		}
	}

	name := plain(m.user.Username)
	if m.user.GitHubLogin != "" {
		name = "@" + m.user.GitHubLogin
	}

	if name == "" {
		name = plain(m.user.Email)
	}

	if name == "" {
		name = "Profile"
	}

	if m.profile != "" && m.profile != "default" {
		name = m.profile + " · " + name
	}

	arrow := " ▾"
	if m.profileOpen {
		arrow = " ▴"
	}

	return ansi.Truncate(name, max(6, (width-4)/2-2), "…") + arrow
}

func (m model) profileArea() hitArea {
	width, _ := m.dimensions()

	labelWidth := ansi.StringWidth(m.profileLabel())
	if m.onDashboard() {
		x := m.contentX() + m.contentWidth() - labelWidth
		if m.dashboardGeometry().sideWidth > 0 {
			x -= 2
		}

		return hitArea{x: x, y: 1, width: labelWidth, height: 1}
	}

	return hitArea{x: width - 2 - labelWidth, y: 1, width: labelWidth, height: 1}
}

func (m model) profileMenuArea() hitArea {
	width, _ := m.dimensions()

	menuWidth := max(4, min(32, width-4))
	if m.onDashboard() {
		x := m.contentX() + m.contentWidth() - menuWidth
		if m.dashboardGeometry().sideWidth > 0 {
			x -= 2
		}

		return hitArea{x: x, y: 2, width: menuWidth, height: 7}
	}

	return hitArea{x: width - 2 - menuWidth, y: 2, width: menuWidth, height: 7}
}

func (m model) drawProfile(lines []string) {
	menu := m.profileMenuArea()
	inside := menu.width - 2

	email := plain(m.user.Email)
	if m.user.GitHubLogin != "" {
		email = "@" + m.user.GitHubLogin
	}

	if email == "" {
		email = "Signed in"
	}

	email = ansi.Truncate(email, inside-2, "…")

	walletLabel := "[Wallet · w / browser · b]"
	if m.walletAddress != "" {
		walletLabel = "Wallet " + m.walletAddress[:min(8, len(m.walletAddress))] + "… · browser b"
	}

	linkLabel := "[Connect GitHub · g]"
	if m.user.GitHubConnected || m.user.GitHubLogin != "" {
		linkLabel = "GitHub connected"
	}

	signIn := "Sign-in: Google"
	if m.user.GitHubConnected || m.user.GitHubLogin != "" {
		signIn = "Sign-in: GitHub"
		if m.user.GoogleConnected {
			signIn = "Sign-in: Google + GitHub"
		}
	}

	rows := []string{
		muted("╭" + strings.Repeat("─", inside) + "╮"),
		muted("│ " + email + strings.Repeat(" ", inside-1-ansi.StringWidth(email)) + "│"),
		muted("│ " + walletLabel + strings.Repeat(" ", max(0, inside-1-ansi.StringWidth(walletLabel))) + "│"),
		muted("│ " + linkLabel + strings.Repeat(" ", max(0, inside-1-ansi.StringWidth(linkLabel))) + "│"),
		muted("│ " + signIn + strings.Repeat(" ", max(0, inside-1-ansi.StringWidth(signIn))) + "│"),
		muted("│") + button(" Log out"+strings.Repeat(" ", inside-8), true) + muted("│"),
		muted("╰" + strings.Repeat("─", inside) + "╯"),
	}
	width, _ := m.dimensions()
	drawOverlay(width, lines, menu, rows)
}

func (m model) updateProfile(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	if m.loading {
		return m, nil
	}

	switch msg.String() {
	case "b":
		m.profileOpen, m.loading, m.err = false, true, nil
		m.notice = "Choose Phantom or Solflare in your browser."
		return m, m.connectBrowserWallet()
	case "g":
		return m.beginGitHubLink()
	case "w":
		m.profileOpen, m.loading, m.err = false, true, nil
		return m, m.connectWallet()
	case "enter", "l":
		return m.beginLogout()
	case "esc", "p", "q":
		m.profileOpen = false
	}

	return m, nil
}

func (m model) beginGitHubLink() (tea.Model, tea.Cmd) {
	if m.user.GitHubConnected || m.user.GitHubLogin != "" {
		return m, nil
	}

	m.profileOpen, m.loading, m.loggingIn, m.err = false, true, true, nil
	m.notice = "Connect GitHub in your browser. Your tasks and wallet stay with this account."

	return m, m.linkGitHub()
}

func (m model) beginLogout() (tea.Model, tea.Cmd) {
	m.stopWorkspace()
	m.stopInvites()
	m.profileOpen, m.loading, m.err = false, true, nil

	return m, m.logout()
}
