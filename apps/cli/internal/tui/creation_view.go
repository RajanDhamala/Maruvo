package tui

import (
	"image/color"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

func (m model) formView() tea.View {
	width, height := m.dimensions()
	if width < 48 || height < 16 {
		return m.frame(nil, "")
	}

	l := m.formLayout()
	label := dashboardChoice(m.profileLabel(), m.profileOpen)
	header := align(accent("MARUVO")+muted("  task exchange"), label, width-4)

	lines := []string{
		"", "  " + header, "",
		strings.Repeat(" ", m.contentX()) + muted("‹ Back") + "   " + bold("New task"), "",
	}
	if m.form.targetWorker != nil {
		lines[3] += muted("  → " + plain(m.form.targetName))
	}

	for y := 0; y < m.bodyHeight(); y++ {
		row := ""
		if y < len(l.rows) {
			row = l.rows[y]
		}

		lines = append(lines, strings.Repeat(" ", m.contentX())+row)
	}

	status, help := "", l.footer
	if m.err != nil {
		status = warning(plain(m.err.Error()))
	} else if m.notice != "" {
		status = accent(plain(m.notice))
	}

	if m.picker.open {
		help = "Arrows select · Tab time · Enter apply · Esc cancel"
	} else if m.profileOpen {
		help = "w wallet · Enter log out · Esc close"
	}

	path := m.homePath
	if path == "" {
		path = currentHomePath()
	}

	lines = append(lines,
		"  "+ansi.Truncate(status, width-4, "…"), "",
		"  "+muted(compactHint(help, width-4)),
		"  "+muted(ansi.Truncate(plain(path), width-4, "…")),
	)
	if m.profileOpen {
		m.drawProfile(lines)
	}

	if m.picker.open {
		m.drawDeadline(lines)
	}

	background := "\x1b[48;2;10;10;10m"

	for i, row := range lines {
		row = ansi.Truncate(row, width, "")
		lines[i] = background + strings.ReplaceAll(row, "\x1b[0m", "\x1b[0m"+background) +
			strings.Repeat(" ", max(0, width-ansi.StringWidth(row))) + "\x1b[0m"
	}

	view := tea.NewView(strings.Join(lines, "\n"))
	view.AltScreen = true
	view.MouseMode = tea.MouseModeCellMotion
	view.BackgroundColor = color.RGBA{R: 10, G: 10, B: 10, A: 255}
	view.ForegroundColor = color.RGBA{R: 238, G: 238, B: 238, A: 255}

	return view
}
