package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/x/ansi"
)

func (m model) deadlineArea() hitArea {
	width, height := m.dimensions()
	popupWidth := max(4, min(52, width-4))

	return hitArea{x: (width - popupWidth) / 2, y: max(0, (height-15)/2), width: popupWidth, height: 15}
}

func (m model) deadlineLayout() postLayout {
	p := m.picker
	width := m.deadlineArea().width - 4

	zoneDate := p.date
	if value, message := p.value(); message == "" {
		zoneDate = value
	}

	title := "Accept by"
	if p.delivery {
		title = "Deliver by"
	}

	l := postLayout{
		rows: []string{align(bold(title), muted("Local · UTC"+zoneDate.Format("-07:00")), width)},
	}
	x := (width - 28) / 2
	month := p.date.Format("January 2006")
	gap := 22 - ansi.StringWidth(month)
	l.rows = append(
		l.rows,
		strings.Repeat(
			" ",
			x,
		)+button(
			" ‹ ",
			false,
		)+strings.Repeat(
			" ",
			gap/2,
		)+bold(
			month,
		)+strings.Repeat(
			" ",
			gap-gap/2,
		)+button(
			" › ",
			false,
		),
	)
	l.hit(x, 1, 3, 1, "month", -1)
	l.hit(x+25, 1, 3, 1, "month", 1)

	weekdays := " Mo  Tu  We  Th  Fr  Sa  Su "
	if p.focus == 0 {
		weekdays = accent(weekdays)
	} else {
		weekdays = muted(weekdays)
	}

	l.rows = append(l.rows, strings.Repeat(" ", x)+weekdays)
	first := time.Date(p.date.Year(), p.date.Month(), 1, 0, 0, 0, 0, time.Local)
	offset := (int(first.Weekday()) + 6) % 7
	days := first.AddDate(0, 1, -1).Day()
	today := day(time.Now())

	for week := 0; week < 6; week++ {
		line := strings.Repeat(" ", x)

		for column := 0; column < 7; column++ {
			number := week*7 + column - offset + 1
			if number < 1 || number > days {
				line += "    "
				continue
			}

			date := first.AddDate(0, 0, number-1)

			text := fmt.Sprintf(" %2d ", number)
			if date.Before(today) {
				line += muted(text)
				continue
			}

			l.hit(x+column*4, len(l.rows), 4, 1, "day", number)

			if number == p.date.Day() {
				line += button(text, true)
			} else if date.Equal(today) {
				line += accent(text)
			} else {
				line += text
			}
		}

		l.rows = append(l.rows, line)
	}

	quickX := 0

	for i, label := range []string{"Tomorrow", "3 days", "1 week"} {
		text := " " + label + " "
		delta := []int{1, 3, 7}[i]
		l.hit(quickX, len(l.rows), len(text), 1, "quick", delta)
		quickX += len(text) + 3
	}

	l.rows = append(
		l.rows,
		button(" Tomorrow ", false)+"   "+button(" 3 days ", false)+"   "+button(" 1 week ", false),
	)
	clock := func(index int) string {
		text := ansi.Truncate(p.clock[index].value, 2, "")

		text += strings.Repeat(" ", max(0, 2-ansi.StringWidth(text)))
		if p.focus == index+1 {
			return "\x1b[7m" + text + "\x1b[0m"
		}

		return bold(text)
	}
	y := len(l.rows)
	l.rows = append(
		l.rows,
		"Time  "+button(
			" − ",
			false,
		)+" "+clock(
			0,
		)+" "+button(
			" + ",
			false,
		)+" : "+button(
			" − ",
			false,
		)+" "+clock(
			1,
		)+" "+button(
			" + ",
			false,
		),
	)
	l.hit(6, y, 3, 1, "hour", -1)
	l.hit(10, y, 2, 1, "clock", 0)
	l.hit(13, y, 3, 1, "hour", 1)
	l.hit(19, y, 3, 1, "minute", -1)
	l.hit(23, y, 2, 1, "clock", 1)
	l.hit(26, y, 3, 1, "minute", 1)

	summary := muted(p.date.Format("Mon, 02 Jan 2006") + " · " + p.clock[0].value + ":" + p.clock[1].value)
	if p.err != "" {
		summary = warning(p.err)
	}

	l.rows = append(l.rows, summary)
	l.buttons([]string{"Apply", "Cancel"}, []string{"apply", "cancel"})

	if p.focus == 4 {
		l.rows[len(l.rows)-1] = button(" Apply ", false) + "   " + button(" Cancel ", true)
	}

	rows := []string{muted("╭" + strings.Repeat("─", width+2) + "╮")}
	for _, line := range l.rows {
		line = ansi.Truncate(line, width, "…")
		rows = append(
			rows,
			muted("│ ")+line+strings.Repeat(" ", max(0, width-ansi.StringWidth(line)))+muted(" │"),
		)
	}

	l.rows = append(rows, muted("╰"+strings.Repeat("─", width+2)+"╯"))
	for i := range l.hits {
		l.hits[i].x += 2
		l.hits[i].y++
	}

	return l
}

func (m model) drawDeadline(lines []string) {
	width, _ := m.dimensions()
	drawOverlay(width, lines, m.deadlineArea(), m.deadlineLayout().rows)
}
