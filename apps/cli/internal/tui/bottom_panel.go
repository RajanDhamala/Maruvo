package tui

import (
	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"strings"
)

// Render the current screen behind a short decision panel.
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
	panel, _ := m.bottomPanelRows(rows, footer)
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

func (m model) bottomPanelRows(rows []string, footer string) ([]string, []int) {
	width, height := m.dimensions()
	inner := max(1, width-4)
	panel := []string{""}
	rowIndexes := []int{-1}
	for i, row := range rows {
		wrapped := strings.Split(ansi.Wrap(row, inner, ""), "\n")
		panel = append(panel, wrapped...)
		for range wrapped {
			rowIndexes = append(rowIndexes, i)
		}
	}
	panel = append(panel, "", compactHint(footer, inner))
	rowIndexes = append(rowIndexes, -1, -1)
	limit := max(3, min(18, height-2))
	if len(panel) > limit {
		panel, rowIndexes = nil, nil
		for i, row := range rows {
			if strings.TrimSpace(ansi.Strip(row)) != "" {
				panel = append(panel, ansi.Truncate(row, inner, "…"))
				rowIndexes = append(rowIndexes, i)
			}
		}
		if len(panel) > limit-1 {
			panel = panel[:limit-1]
			rowIndexes = rowIndexes[:limit-1]
		}
		panel = append(panel, compactHint(footer, inner))
		rowIndexes = append(rowIndexes, -1)
	}
	return panel, rowIndexes
}
