package tui

import (
	"errors"
	"strconv"
	"strings"
	"time"
	"unicode"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/rajandhamala/Maruvo/cli/internal/api"
)

type textField struct {
	value      string
	cursor     int
	limit      int
	digitsOnly bool
}

type postForm struct {
	fields [7]textField
	focus  int
	level  int
}

func newPostForm() postForm {
	deadline := time.Now().Add(24 * time.Hour).Format("2006-01-02 15:04")

	return postForm{fields: [7]textField{
		{limit: 500},
		{value: "0", cursor: 1, limit: 19, digitsOnly: true},
		{value: deadline, cursor: len(deadline), limit: 16},
		{limit: 12000},
		{limit: 4000},
		{limit: 3600},
		{limit: 3600},
	}}
}

func (f *textField) insert(text string) {
	text = strings.Map(func(r rune) rune {
		if r == '\n' || r == '\r' || r == '\t' {
			return ' '
		}

		if unicode.IsControl(r) {
			return -1
		}

		return r
	}, ansi.Strip(text))
	if text == "" {
		return
	}

	if f.digitsOnly && strings.Trim(text, "0123456789") != "" {
		return
	}

	runes := []rune(f.value)

	added := []rune(text)
	if len(runes)+len(added) > f.limit {
		return
	}

	runes = append(runes[:f.cursor], append(added, runes[f.cursor:]...)...)
	f.value = string(runes)
	f.cursor += len(added)
}

func (f *textField) key(msg tea.KeyPressMsg) {
	runes := []rune(f.value)

	switch msg.String() {
	case "left":
		f.cursor = max(0, f.cursor-1)
	case "right":
		f.cursor = min(len(runes), f.cursor+1)
	case "home":
		f.cursor = 0
	case "end":
		f.cursor = len(runes)
	case "backspace":
		if f.cursor > 0 {
			f.value = string(append(runes[:f.cursor-1], runes[f.cursor:]...))
			f.cursor--
		}
	case "delete":
		if f.cursor < len(runes) {
			f.value = string(append(runes[:f.cursor], runes[f.cursor+1:]...))
		}
	case "ctrl+u":
		f.value, f.cursor = "", 0
	default:
		f.insert(msg.Text)
	}
}

func (f postForm) payload() (api.CreatePostPayload, error) {
	title := strings.TrimSpace(f.fields[0].value)
	if title == "" {
		return api.CreatePostPayload{}, errors.New("Add a title for your post.")
	}

	cost, err := strconv.ParseInt(strings.TrimSpace(f.fields[1].value), 10, 64)
	if err != nil || cost < 0 {
		return api.CreatePostPayload{}, errors.New("Cost must be a non-negative whole number of lamports.")
	}

	deadline, err := time.ParseInLocation("2006-01-02 15:04", f.fields[2].value, time.Local)
	if err != nil || !deadline.After(time.Now()) {
		return api.CreatePostPayload{}, errors.New("Use a future deadline in YYYY-MM-DD HH:MM format.")
	}

	description := strings.TrimSpace(f.fields[3].value)
	if description == "" {
		return api.CreatePostPayload{}, errors.New("Describe the task instructions.")
	}

	files := func(value string) []string {
		result := []string{}

		for _, name := range strings.Split(value, ",") {
			if name = strings.TrimSpace(name); name != "" {
				result = append(result, name)
			}
		}

		return result
	}

	return api.CreatePostPayload{
		Title:              title,
		CostLamports:       cost,
		EndTime:            deadline.UTC(),
		Level:              levels[f.level],
		Description:        description,
		AcceptanceCriteria: strings.TrimSpace(f.fields[4].value),
		InputFiles:         files(f.fields[5].value),
		ExpectedOutputs:    files(f.fields[6].value),
	}, nil
}

func (m model) updateForm(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		return m.openPosts(m.own)
	case "ctrl+s":
		return m.submitPost()
	case "tab", "down":
		m.form.focus = (m.form.focus + 1) % 8
	case "shift+tab", "up":
		m.form.focus = (m.form.focus + 7) % 8
	case "enter":
		if m.form.focus == 2 {
			return m.openDeadline(), nil
		}

		if m.form.focus == 7 {
			return m.submitPost()
		}

		m.form.focus++
	default:
		if m.form.focus == 7 {
			switch msg.String() {
			case "left":
				m.form.level = (m.form.level + 2) % 3
			case "right":
				m.form.level = (m.form.level + 1) % 3
			}
		} else if m.form.focus == 2 {
			if msg.String() == "space" || msg.String() == " " {
				return m.openDeadline(), nil
			}
		} else {
			m.form.fields[m.form.focus].key(msg)
		}
	}

	return m, nil
}

func (m model) submitPost() (tea.Model, tea.Cmd) {
	payload, err := m.form.payload()
	if err != nil {
		m.err = err
		return m, nil
	}

	m.loading, m.err, m.notice = true, nil, ""

	return m, m.createPost(payload)
}
