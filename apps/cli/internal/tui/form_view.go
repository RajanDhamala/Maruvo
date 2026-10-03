package tui

import (
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/charmbracelet/x/ansi"
)

func (m model) formLayout() postLayout {
	width := m.contentWidth()

	l := postLayout{footer: "Tab next · Ctrl+o file · Ctrl+s publish · Esc back"}
	if m.form.focus == 3 && m.form.descriptionSource == descriptionFromFile {
		l.footer = "Enter choose file · → write text · Tab next · Ctrl+s publish"
	} else if m.form.focus == 3 || m.form.focus == 4 {
		l.footer = "Enter newline · Tab next · Ctrl+s publish · Esc back"
	} else if m.form.focus == 2 {
		l.footer = "Enter choose date · Tab next · Ctrl+s publish · Esc back"
	}

	if width < 64 {
		l.footer = "Tab next · Ctrl+o file · Ctrl+s publish"
		if m.form.focus == 3 && m.form.descriptionSource == descriptionFromFile {
			l.footer = "Enter file · → text · Tab · Ctrl+s publish"
		}
	}

	labels := []string{
		"Title",
		"Budget · lamports",
		"Accept by · local time",
		"Description",
		"Expected result · optional",
	}
	placeholders := []string{"A short, specific task title", "0 or more", "Choose an acceptance cutoff",
		"Describe what the worker should do", "What should the finished result achieve?"}
	anchors := [6]int{}
	target := 0
	fieldLabel := func(i int) string {
		if m.form.focus == i && !m.form.importing {
			return accent(labels[i])
		}

		return muted(labels[i])
	}

	textHeight := max(1, min(4, (m.bodyHeight()-7)/2))
	for i, field := range m.form.fields {
		if i == 2 && width >= 64 {
			continue
		}

		anchors[i] = len(l.rows)
		if i == 1 && width >= 64 {
			split := 24
			anchors[2] = anchors[1]
			left, _ := formFieldRows(field, split-3, 1, m.form.focus == 1, placeholders[1])
			right, _ := formFieldRows(m.deadlineField(), width-split, 1, m.form.focus == 2, "")

			l.rows = append(
				l.rows,
				fieldLabel(1)+strings.Repeat(" ", split-ansi.StringWidth(labels[1]))+fieldLabel(2),
			)
			for j := range left {
				l.rows = append(l.rows, left[j]+"   "+right[j])
			}

			amount, _ := strconv.ParseInt(field.value, 10, 64)
			l.rows = append(l.rows, muted(taskBudget(amount)), "")
			l.hit(0, anchors[1], split-3, 4, "field", 1)
			l.hit(split, anchors[2], width-split, 4, "field", 2)

			if m.form.focus == 1 || m.form.focus == 2 {
				target = anchors[1] + 3
			}

			continue
		}

		label := fieldLabel(i)
		if i == 3 {
			file := button(" File ", m.form.descriptionSource == descriptionFromFile)
			written := button(" Write text ", m.form.descriptionSource == descriptionFromText)
			label = align(label, file+"  "+written, width)
			x := width - ansi.StringWidth(file) - 2 - ansi.StringWidth(written)
			l.hit(x, len(l.rows), ansi.StringWidth(file), 1, "description-source", descriptionFromFile)
			l.hit(x+ansi.StringWidth(file)+2, len(l.rows), ansi.StringWidth(written), 1,
				"description-source", descriptionFromText)
		}

		l.rows = append(l.rows, label)
		if i == 3 && m.form.importing {
			target = m.descriptionSearchRows(&l)
		} else if i == 3 && m.form.descriptionSource == descriptionFromFile {
			m.descriptionFileRows(&l, width)

			if m.form.focus == 3 {
				target = len(l.rows) - 1
			}
		} else {
			height := 1
			if field.multiline {
				height = textHeight
				if i == 4 {
					height = min(3, height)
				}
			}

			if i == 2 {
				field = m.deadlineField()
			}

			rows, _ := formFieldRows(field, width, height, m.form.focus == i, placeholders[i])
			l.rows = append(l.rows, rows...)
			l.hit(0, anchors[i], width, len(rows)+1, "field", i)

			if m.form.focus == i && !m.form.importing {
				target = len(l.rows) - 1
			}

			if i == 1 {
				amount, _ := strconv.ParseInt(field.value, 10, 64)
				l.rows = append(l.rows, muted(taskBudget(amount)))
			}

			if i == 3 && m.form.focus == 3 {
				counter := fmt.Sprintf(
					"%d / 12,000 chars · %s / 32 KiB",
					utf8.RuneCountInString(field.value),
					fileSize(int64(len(field.value))),
				)
				l.rows = append(l.rows, muted(ansi.Truncate(counter, width, "…")))
			}
		}

		l.rows = append(l.rows, "")
	}

	anchors[5] = len(l.rows)

	label := muted("Difficulty")
	if m.form.focus == 5 {
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

	l.rows = append(l.rows, line)
	if m.form.focus >= 5 && !m.form.importing {
		target = len(l.rows) - 1
	}

	available := max(1, m.bodyHeight()-2)
	start := max(0, min(target-available+1, len(l.rows)-available))

	for i := range m.form.fields {
		if i == 2 && width >= 64 {
			continue
		}

		next := anchors[i+1]

		focused := m.form.focus == i
		if i == 1 && width >= 64 {
			next = anchors[3]
			focused = focused || m.form.focus == 2
		}

		if !focused && start > anchors[i] && start < next {
			start = next
		}
	}

	end := min(len(l.rows), start+available)
	for _, hit := range l.hits {
		if (hit.action == "field" || hit.action == "description-card") && hit.index != m.form.focus &&
			hit.y >= start && hit.y < end && hit.y+hit.height > end {
			end = hit.y
		}
	}

	l.rows = l.rows[start:end]
	for i := range l.hits {
		l.hits[i].y -= start
		if l.hits[i].y < 0 || l.hits[i].y+l.hits[i].height > len(l.rows) {
			l.hits[i].height = 0
		}
	}

	l.rows = append(l.rows, "")
	publish := " Publish task → "

	primary := accent("[" + strings.TrimSpace(publish) + "]")
	if m.form.focus == 6 {
		primary = button(publish, true)
	}

	if m.loading {
		if m.form.importing {
			l.footer = "Reading description…"
		} else {
			publish = " Publishing… "
			l.footer = "Creating your task…"
		}

		primary = button(publish, false)
	}

	cancel := button(" Cancel ", m.form.focus == 7)

	l.hit(0, len(l.rows), ansi.StringWidth(primary), 1, "publish", 0)
	l.hit(ansi.StringWidth(primary)+3, len(l.rows), ansi.StringWidth(cancel), 1, "back", 0)
	l.rows = append(l.rows, primary+"   "+cancel)

	return l
}

func (m model) deadlineField() textField {
	field := m.form.fields[2]
	if deadline, err := time.ParseInLocation("2006-01-02 15:04", field.value, time.Local); err == nil {
		field.value = deadline.Format("02 Jan 2006, 15:04")
	}

	field.value += " ▾"
	field.cursor = 0

	return field
}

func formFieldRows(field textField, width, height int, active bool, placeholder string) ([]string, int) {
	inner := width - 1

	rows, cursor := field.textRows(inner, height, active, placeholder)
	if !field.multiline {
		rows = []string{field.inputPlaceholder(inner, active, placeholder)}
		cursor = 0
	}

	if active && field.value == "" && placeholder != "" {
		text := []rune(placeholder)
		rows[0] = inputRow("\x1b[7m"+string(text[0])+"\x1b[0m"+muted(string(text[1:])), inner)
	}

	rows = append([]string{inputRow("", inner)}, rows...)
	rows = append(rows, inputRow("", inner))

	rail := muted("│")
	if active {
		rail = accent("▎")
	}

	for i := range rows {
		rows[i] = rail + rows[i]
	}

	return rows, cursor + 1
}

func (m model) descriptionFileRows(l *postLayout, width int) {
	active := m.form.focus == 3

	rail := muted("│")
	if active {
		rail = accent("▎")
	}

	field := m.form.fields[3]

	meta := fmt.Sprintf(
		"%s · %d characters",
		fileSize(int64(len(field.value))),
		utf8.RuneCountInString(field.value),
	)
	rows := []string{"", "No file selected.", ""}
	action := " Choose file "

	if m.form.descriptionFile != "" {
		rows = []string{"", "\uf15c " + bold(plain(m.form.descriptionFile)), muted(meta), ""}
		action = " Replace file "
	}

	if m.bodyHeight() < 11 {
		rows = rows[1 : len(rows)-1]
	}

	l.hit(0, len(l.rows), width, len(rows), "description-card", 3)

	for _, row := range rows {
		l.rows = append(l.rows, rail+inputRow(row, width-1))
	}

	choose := button(action, active)
	l.hit(0, len(l.rows), ansi.StringWidth(choose), 1, "description-import", 0)

	if m.form.descriptionFile != "" {
		remove := button(" Remove ", false)
		l.hit(ansi.StringWidth(choose)+3, len(l.rows), ansi.StringWidth(remove), 1, "description-remove", 0)
		choose += "   " + remove
	}

	l.rows = append(l.rows, choose)
}

func (f textField) input(width int, active bool) string {
	return f.inputPlaceholder(width, active, "Describe your task")
}

func (f textField) inputPlaceholder(width int, active bool, placeholder string) string {
	value := f.render(width-2, active)
	if !active && f.value == "" {
		value = muted(placeholder)
		if f.digitsOnly {
			value = muted("0 or more")
		}
	}

	return inputRow(value, width)
}

func inputRow(value string, width int) string {
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
