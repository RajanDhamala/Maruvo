package tui

import (
	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"strings"
)

// Render the current screen behind a short, keyboard-owned decision panel.
func (m model) bottomPanel(rows []string, footer string) tea.View {
	width, height := m.dimensions()
	background := m
	background.invitations.pending = nil
	background.workSetup.open = false
	background.permissions.open = false
	background.localAgent.approval = nil
	background.commands.open = false
	view := background.View()
	inner := max(1, width-4)
	panel := []string{""}
	for _, row := range rows {
		panel = append(panel, strings.Split(ansi.Wrap(row, inner, ""), "\n")...)
	}
	footer = compactHint(footer, inner)
	panel = append(panel, "", footer)
	limit := max(3, min(18, height-2))
	if len(panel) > limit {
		panel = nil
		for _, row := range rows {
			if strings.TrimSpace(ansi.Strip(row)) != "" {
				panel = append(panel, ansi.Truncate(row, inner, "…"))
			}
		}
		if len(panel) > limit-1 {
			panel = panel[:limit-1]
		}
		panel = append(panel, footer)
	}
	top := max(0, height-len(panel))
	lines := strings.Split(view.Content, "\n")
	if len(lines) > top {
		lines = lines[:top]
	}
	for len(lines) < top {
		lines = append(lines, "")
	}
	for _, row := range panel {
		text := ansi.Strip(ansi.Truncate(row, inner, "…"))
		style := "\x1b[48;2;55;55;55m\x1b[38;2;235;235;235m"
		if strings.HasPrefix(strings.TrimSpace(text), "›") {
			style = "\x1b[48;2;90;155;235m\x1b[38;2;0;0;0m"
		}
		padded := "  " + text + strings.Repeat(" ", max(0, width-2-ansi.StringWidth(text)))
		lines = append(lines, style+padded+"\x1b[0m")
	}
	view.Content = strings.Join(lines, "\n")
	view.Cursor = nil
	return view
}
