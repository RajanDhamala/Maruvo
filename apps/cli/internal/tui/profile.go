package tui

import (
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

func (m model) profileLabel() string {
	width, _ := m.dimensions()

	name := plain(m.user.Username)
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

	return hitArea{x: width - 2 - labelWidth, y: 1, width: labelWidth, height: 1}
}

func (m model) profileMenuArea() hitArea {
	width, _ := m.dimensions()
	menuWidth := max(4, min(32, width-4))

	return hitArea{x: width - 2 - menuWidth, y: 2, width: menuWidth, height: 5}
}

func (m model) drawProfile(lines []string) {
	menu := m.profileMenuArea()
	inside := menu.width - 2

	email := plain(m.user.Email)
	if email == "" {
		email = "Signed in"
	}

	email = ansi.Truncate(email, inside-2, "…")

	walletLabel := "[Connect wallet]"
	if m.walletAddress != "" {
		walletLabel = "Wallet " + m.walletAddress[:min(8, len(m.walletAddress))] + "…"
	}

	rows := []string{
		muted("╭" + strings.Repeat("─", inside) + "╮"),
		muted("│ " + email + strings.Repeat(" ", inside-1-ansi.StringWidth(email)) + "│"),
		muted("│ " + walletLabel + strings.Repeat(" ", max(0, inside-1-ansi.StringWidth(walletLabel))) + "│"),
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

func (m model) beginLogout() (tea.Model, tea.Cmd) {
	m.stopWorkspace()
	m.profileOpen, m.loading, m.err = false, true, nil

	return m, m.logout()
}
