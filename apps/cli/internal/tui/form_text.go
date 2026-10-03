package tui

import (
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

type fieldLine struct {
	start, end int
}

func (f textField) textLines(width int) ([]fieldLine, int) {
	runes := []rune(f.value)
	lines := []fieldLine{}
	start, cells := 0, 0

	for i, r := range runes {
		if r == '\n' {
			lines = append(lines, fieldLine{start, i})
			start, cells = i+1, 0

			continue
		}

		size := ansi.StringWidth(fieldText(string(r)))
		if cells > 0 && cells+size > max(1, width-3) {
			lines = append(lines, fieldLine{start, i})
			start, cells = i, 0
		}

		cells += size
	}

	lines = append(lines, fieldLine{start, len(runes)})
	cursorLine := 0

	for i, line := range lines {
		if f.cursor >= line.start && f.cursor <= line.end {
			cursorLine = i
		}
	}

	return lines, cursorLine
}

func fieldText(text string) string {
	return strings.ReplaceAll(strings.ReplaceAll(text, "\t", "    "), "\r", "")
}

func (f textField) textRows(width, height int, active bool, placeholder string) ([]string, int) {
	lines, cursorLine := f.textLines(width)

	start := 0
	if active {
		start = max(0, cursorLine-height+1)
	}

	runes := []rune(f.value)

	rows := make([]string, height)
	for i := range rows {
		text := ""

		if i+start < len(lines) {
			line := lines[i+start]

			text = fieldText(string(runes[line.start:line.end]))
			if active && i+start == cursorLine {
				cursor := min(max(f.cursor, line.start), line.end)

				tail, char := cursor, " "
				if cursor < line.end {
					char, tail = fieldText(string(runes[cursor])), cursor+1
				}

				text = fieldText(string(runes[line.start:cursor])) + "\x1b[7m" + char + "\x1b[0m" +
					fieldText(string(runes[tail:line.end]))
			}
		}

		if f.value == "" && i == 0 && !active {
			text = muted(ansi.Truncate(placeholder, max(1, width-2), "…"))
		}

		rows[i] = inputRow(text, width)
	}

	return rows, min(height-1, cursorLine-start)
}

func (f *textField) cursorAt(width, row, column int) {
	lines, _ := f.textLines(width)
	line := lines[min(max(0, row), len(lines)-1)]
	runes := []rune(f.value)

	f.cursor = line.start
	for f.cursor < line.end {
		cells := ansi.StringWidth(fieldText(string(runes[f.cursor])))
		if column < cells {
			break
		}

		column -= cells
		f.cursor++
	}
}

func (f *textField) multilineKey(msg tea.KeyPressMsg, width int) bool {
	lines, row := f.textLines(width)
	column := ansi.StringWidth(fieldText(string([]rune(f.value)[lines[row].start:f.cursor])))

	switch msg.String() {
	case "up":
		f.cursorAt(width, row-1, column)
	case "down":
		f.cursorAt(width, row+1, column)
	case "home":
		f.cursor = lines[row].start
	case "end":
		f.cursor = lines[row].end
	case "ctrl+home":
		f.cursor = 0
	case "ctrl+end":
		f.cursor = len([]rune(f.value))
	default:
		return false
	}

	return true
}
