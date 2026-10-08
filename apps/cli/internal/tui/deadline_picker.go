package tui

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
)

type deadlinePicker struct {
	open     bool
	date     time.Time
	clock    [2]textField
	focus    int
	replace  bool
	err      string
	delivery bool
}

func day(date time.Time) time.Time {
	return time.Date(date.Year(), date.Month(), date.Day(), 0, 0, 0, 0, date.Location())
}

func (m model) openDeadline() model {
	date, err := time.ParseInLocation("2006-01-02 15:04", m.form.fields[2].value, time.Local)
	if m.recovering == "reopen" {
		date, err = m.recoveryDeadline, nil
	} else {
		m.form.focus = 2
	}

	if err != nil || !date.After(time.Now()) {
		date = time.Now().Add(24 * time.Hour)
	}

	m.profileOpen = false
	m.picker = deadlinePicker{open: true, date: day(date), clock: [2]textField{
		{value: date.Format("15"), cursor: 2, limit: 2},
		{value: date.Format("04"), cursor: 2, limit: 2},
	}}

	return m
}

func (m model) openDeliveryDeadline() model {
	date, err := time.ParseInLocation("2006-01-02 15:04", m.form.timings[1].value, time.Local)
	if m.recovering == "reopen" {
		date, err = m.recoveryDelivery, nil
	}

	if err != nil || !date.After(time.Now()) {
		acceptBy, _ := time.ParseInLocation("2006-01-02 15:04", m.form.fields[2].value, time.Local)

		funding, _ := strconv.ParseInt(m.form.timings[0].value, 10, 64)
		if m.recovering == "reopen" {
			acceptBy, funding = m.recoveryDeadline, m.posts[m.selected].FundingWindowSeconds/3600
		}

		date = acceptBy.Add(time.Duration(funding+24) * time.Hour)
		if !date.After(time.Now()) {
			date = time.Now().Add(72 * time.Hour)
		}
	}

	m.profileOpen = false
	m.picker = deadlinePicker{open: true, delivery: true, date: day(date), clock: [2]textField{
		{value: date.Format("15"), cursor: 2, limit: 2},
		{value: date.Format("04"), cursor: 2, limit: 2},
	}}

	return m
}

func (p *deadlinePicker) selectDate(date time.Time) {
	p.date = day(date)
	if today := day(time.Now()); p.date.Before(today) {
		p.date = today
	}

	p.err = ""
	p.setFocus(0)
}

func (p *deadlinePicker) changeMonth(delta int) {
	first := time.Date(p.date.Year(), p.date.Month()+time.Month(delta), 1, 0, 0, 0, 0, time.Local)

	today := time.Now()
	if first.Before(time.Date(today.Year(), today.Month(), 1, 0, 0, 0, 0, time.Local)) {
		return
	}

	days := first.AddDate(0, 1, -1).Day()
	p.selectDate(first.AddDate(0, 0, min(p.date.Day(), days)-1))
}

func (p *deadlinePicker) setFocus(focus int) {
	p.focus, p.replace = focus, true
}

func (p *deadlinePicker) changeTime(index, delta int) {
	value, _ := strconv.Atoi(p.clock[index].value)

	limit := 24
	if index == 1 {
		limit = 60
	}

	value = ((value+delta)%limit + limit) % limit
	p.clock[index].value, p.clock[index].cursor = fmt.Sprintf("%02d", value), 2
	p.err = ""
	p.setFocus(index + 1)
}

func (p *deadlinePicker) insert(text string) {
	if p.focus != 1 && p.focus != 2 {
		return
	}

	text = strings.TrimSpace(text)
	if text == "" || len(text) > 2 || strings.Trim(text, "0123456789") != "" {
		return
	}

	field := &p.clock[p.focus-1]
	if p.replace {
		field.value, field.cursor = "", 0
		p.replace = false
	}

	field.insert(text)

	p.err = ""
}

func (p deadlinePicker) value() (time.Time, string) {
	hour, hourErr := strconv.Atoi(p.clock[0].value)

	minute, minuteErr := strconv.Atoi(p.clock[1].value)
	if hourErr != nil || minuteErr != nil || hour < 0 || hour > 23 || minute < 0 || minute > 59 {
		return time.Time{}, "Hours 0–23; minutes 0–59."
	}

	date := time.Date(p.date.Year(), p.date.Month(), p.date.Day(), hour, minute, 0, 0, time.Local)
	if date.Hour() != hour || date.Minute() != minute {
		return time.Time{}, "That time does not exist locally."
	}

	return date, ""
}

func (m model) applyDeadline() (tea.Model, tea.Cmd) {
	date, message := m.picker.value()
	if message == "" && !date.After(time.Now()) {
		message = "Choose a future date and time."
	}

	if message != "" {
		m.picker.err = message
		return m, nil
	}

	if m.picker.delivery {
		if m.recovering == "reopen" {
			m.recoveryDelivery = date
		} else {
			m.form.timings[1].value = date.Format("2006-01-02 15:04")
			m.form.timings[1].cursor = len(m.form.timings[1].value)
			m.form.deliveryDefault = false
		}
	} else if m.recovering == "reopen" {
		m.recoveryDeadline = date
	} else {
		field := &m.form.fields[2]
		field.value = date.Format("2006-01-02 15:04")
		field.cursor = len(field.value)

		m.form.syncDefaultDelivery()
	}

	m.picker.open, m.err = false, nil

	return m, nil
}

func (m model) updateDeadline(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	p := &m.picker

	switch msg.String() {
	case "esc":
		p.open = false
	case "enter", "ctrl+s":
		if p.focus == 4 {
			p.open = false
			return m, nil
		}

		return m.applyDeadline()
	case "tab":
		p.setFocus((p.focus + 1) % 5)
	case "shift+tab":
		p.setFocus((p.focus + 4) % 5)
	case "pgup":
		p.changeMonth(-1)
	case "pgdown":
		p.changeMonth(1)
	case "left", "right", "up", "down":
		key := msg.String()

		delta := 1
		if key == "left" || key == "up" {
			delta = -1
		}

		switch p.focus {
		case 0:
			if key == "up" || key == "down" {
				delta *= 7
			}

			p.selectDate(p.date.AddDate(0, 0, delta))
		case 1, 2:
			if key == "up" || key == "down" {
				p.changeTime(p.focus-1, -delta)
			} else {
				p.setFocus(3 - p.focus)
			}
		case 3, 4:
			p.setFocus(7 - p.focus)
		}
	case "backspace", "delete", "ctrl+u":
		if p.focus == 1 || p.focus == 2 {
			p.clock[p.focus-1].key(msg)
			p.replace, p.err = false, ""
		}
	default:
		p.insert(msg.Text)
	}

	return m, nil
}

func (m model) updateDeadlineMouse(msg tea.MouseClickMsg) (tea.Model, tea.Cmd) {
	area := m.deadlineArea()

	x, y := msg.X-area.x, msg.Y-area.y
	if x < 0 || x >= area.width || y < 0 || y >= area.height {
		m.picker.open = false
		return m, nil
	}

	for _, hit := range m.deadlineLayout().hits {
		if x < hit.x || x >= hit.x+hit.width || y < hit.y || y >= hit.y+hit.height {
			continue
		}

		switch hit.action {
		case "day":
			m.picker.selectDate(
				time.Date(m.picker.date.Year(), m.picker.date.Month(), hit.index, 0, 0, 0, 0, time.Local),
			)
			m.picker.setFocus(0)
		case "month":
			m.picker.changeMonth(hit.index)
		case "quick":
			m.picker.selectDate(day(time.Now()).AddDate(0, 0, hit.index))
		case "clock":
			m.picker.setFocus(hit.index + 1)
		case "hour", "minute":
			index := 0
			if hit.action == "minute" {
				index = 1
			}

			m.picker.changeTime(index, hit.index)
		case "apply":
			return m.applyDeadline()
		case "cancel":
			m.picker.open = false
		}

		return m, nil
	}

	return m, nil
}
