package tui

import (
	"strings"

	"github.com/charmbracelet/x/ansi"
)

var tabNames = []string{"1 Feed", "2 My Tasks", "3 New Task"}

type hitArea struct {
	x, y, width, height int
	action              string
	index               int
}

type postLayout struct {
	rows   []string
	hits   []hitArea
	footer string
}

func (l *postLayout) hit(x, y, width, height int, action string, index int) {
	l.hits = append(l.hits, hitArea{x, y, width, height, action, index})
}

func (l *postLayout) buttons(labels, actions []string) {
	x, y := 0, len(l.rows)
	line := ""

	for i, label := range labels {
		if i != 0 {
			line += "   "
			x += 3
		}

		text := " " + label + " "
		line += button(text, i == 0)
		l.hit(x, y, ansi.StringWidth(text), 1, actions[i], 0)
		x += ansi.StringWidth(text)
	}

	l.rows = append(l.rows, line)
}

func (l *postLayout) wrappedButtons(width int, labels, actions []string) {
	x, line := 0, ""

	for i, label := range labels {
		text := " " + label + " "

		cells := ansi.StringWidth(text)
		if x > 0 && x+cells > width {
			l.rows = append(l.rows, line)
			x, line = 0, ""
		}

		l.hit(x, len(l.rows), cells, 1, actions[i], 0)
		line += button(text, i == 0) + "  "
		x += cells + 2
	}

	l.rows = append(l.rows, line)
}

func (m model) contentWidth() int {
	width, _ := m.dimensions()
	if m.onDashboard() {
		return max(1, width-4)
	}

	if m.screen == workspaceScreen {
		return max(1, width-4)
	}

	if m.screen == newPostScreen {
		return max(1, min(96, width-4))
	}

	return max(1, min(88, width-4))
}

func (m model) bodyStart() int {
	if m.onDashboard() {
		return 3
	}

	if m.screen == workspaceScreen {
		return 4
	}

	if m.screen == newPostScreen {
		return 5
	}

	return 6
}

func (m model) bodyHeight() int {
	_, height := m.dimensions()
	if m.onDashboard() {
		return height - m.bodyStart()
	}

	return height - m.bodyStart() - 4
}

func (m model) contentX() int {
	width, _ := m.dimensions()
	return 2 + (width-4-m.contentWidth())/2
}

func align(left, right string, width int) string {
	right = ansi.Truncate(right, max(1, width/2), "…")
	left = ansi.Truncate(left, max(1, width-ansi.StringWidth(right)-2), "…")

	return left + strings.Repeat(" ", max(2, width-ansi.StringWidth(left)-ansi.StringWidth(right))) + right
}

func button(text string, active bool) string {
	if active {
		return "\x1b[1;30;46m" + text + "\x1b[0m"
	}

	return "\x1b[2m[" + strings.TrimSpace(text) + "]\x1b[0m"
}

func drawOverlay(width int, lines []string, area hitArea, rows []string) {
	for i, row := range rows {
		y := area.y + i
		left := ansi.Cut(lines[y], 0, area.x)
		left += strings.Repeat(" ", max(0, area.x-ansi.StringWidth(left)))
		lines[y] = left + "\x1b[0m" + row + ansi.Cut(lines[y], area.x+area.width, width)
	}
}
