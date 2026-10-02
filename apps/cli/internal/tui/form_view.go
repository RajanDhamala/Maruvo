package tui

import (
	"strings"
	"time"

	"github.com/charmbracelet/x/ansi"
)

func (m model) formLayout() postLayout {
	width := m.contentWidth()
	_, height := m.dimensions()
	l := postLayout{
		rows:   []string{bold("Create a post"), muted("What do you need help with?"), ""},
		footer: "Click to edit   Tab next   Ctrl+s publish   Esc back",
	}
	anchors := [8]int{}
	labels := []string{
		"Title",
		"Cost · lamports",
		"Deadline · local time",
		"Task instructions",
		"Acceptance criteria",
		"Input filenames · comma separated",
		"Output filenames · comma separated",
	}

	fieldLabel := func(i int) string {
		if m.form.focus == i {
			return accent(labels[i])
		}

		return muted(labels[i])
	}
	for i, field := range m.form.fields {
		if i == 1 && width >= 64 {
			split := 24
			anchors[1], anchors[2] = len(l.rows), len(l.rows)
			l.rows = append(l.rows,
				fieldLabel(1)+strings.Repeat(" ", split-ansi.StringWidth(labels[1]))+fieldLabel(2),
				field.input(split-3, m.form.focus == 1)+"   "+m.deadlineInput(width-split),
				strings.Repeat(" ", split)+muted("Click / Enter to choose"), "")
			l.hit(0, anchors[1], split-3, 2, "field", 1)
			l.hit(split, anchors[2], width-split, 2, "field", 2)

			continue
		}

		if i == 2 && width >= 64 {
			continue
		}

		anchors[i] = len(l.rows)
		label := fieldLabel(i)

		input := field.input(width, m.form.focus == i)
		if i == 2 {
			label += muted(" · Enter to choose")
			input = m.deadlineInput(width)
		}

		l.rows = append(l.rows, label, input, "")
		l.hit(0, anchors[i], width, 2, "field", i)
	}

	anchors[7] = len(l.rows)

	label := muted("Difficulty")
	if m.form.focus == 7 {
		label = accent("Difficulty")
	}

	l.rows = append(l.rows, label)
	line, x := "", 0

	for i, level := range levels {
		text := " " + level + " "
		line += button(text, i == m.form.level) + "  "
		l.hit(x, len(l.rows), ansi.StringWidth(text), 1, "level", i)
		x += ansi.StringWidth(text) + 2
	}

	l.rows = append(l.rows, line, "")
	l.buttons([]string{"Publish post", "Cancel"}, []string{"publish", "back"})

	l.rows[len(l.rows)-1] = "\x1b[1;32m[Publish post]\x1b[0m   " + button(" Cancel ", false)
	if len(l.rows) > height-10 {
		available := max(1, height-12)

		target := anchors[m.form.focus] + 1
		if m.form.focus == 7 {
			target = len(l.rows) - 1
		}

		start := min(max(0, target-1-available), max(0, len(l.rows)-2-available))

		l.rows = append(l.rows[:2], l.rows[2+start:min(len(l.rows), 2+start+available)]...)
		for i := range l.hits {
			l.hits[i].y -= start
			if l.hits[i].y < 2 {
				l.hits[i].height = 0
			}
		}
	}

	if m.loading {
		l.footer = "Creating your post..."
	}

	return l
}

func (m model) deadlineInput(width int) string {
	field := m.form.fields[2]
	if deadline, err := time.ParseInLocation("2006-01-02 15:04", field.value, time.Local); err == nil {
		field.value = deadline.Format("02 Jan 2006, 15:04")
	}

	field.value += " ▾"

	return field.input(width, false)
}

func (f textField) input(width int, active bool) string {
	value := f.render(width-2, active)
	if !active && f.value == "" {
		value = muted("Describe your task")
		if f.digitsOnly {
			value = muted("0 or more")
		}
	}

	value = ansi.Truncate(value, max(1, width-2), "…")
	background := "\x1b[48;5;236m"

	return background + " " + strings.ReplaceAll(
		value,
		"\x1b[0m",
		"\x1b[0m"+background,
	) + strings.Repeat(
		" ",
		max(1, width-1-ansi.StringWidth(value)),
	) + "\x1b[0m"
}

func (f textField) render(width int, active bool) string {
	if !active {
		return ansi.Truncate(ansi.Strip(f.value), max(1, width), "…")
	}

	runes := []rune(f.value)
	start := f.visibleStart(width)

	before, after, cursor := string(runes[start:f.cursor]), "", " "
	if f.cursor < len(runes) {
		cursor, after = string(runes[f.cursor]), string(runes[f.cursor+1:])
	}

	return ansi.Truncate(before+"\x1b[7m"+cursor+"\x1b[0m"+after, max(1, width), "")
}

func (f textField) visibleStart(width int) int {
	runes := []rune(f.value)

	start := 0
	for start < f.cursor && ansi.StringWidth(string(runes[start:f.cursor])) >= max(1, width-1) {
		start++
	}

	return start
}
