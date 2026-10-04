package tui

import (
	"errors"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/rajandhamala/Maruvo/cli/internal/api"
)

type textField struct {
	value      string
	cursor     int
	limit      int
	byteLimit  int
	digitsOnly bool
	multiline  bool
}

type postForm struct {
	fields            [5]textField
	focus             int
	level             int
	importing         bool
	path              textField
	fileSearch        descriptionFileSearch
	descriptionFile   string
	descriptionSource int
	timings           [3]textField
	timingOpen        bool
	timingFocus       int
}

const (
	descriptionFromFile = iota
	descriptionFromText
)

func newPostForm() postForm {
	deadline := time.Now().Add(24 * time.Hour).Format("2006-01-02 15:04")

	return postForm{timings: [3]textField{
		{value: "24", cursor: 2, limit: 3, digitsOnly: true},
		{limit: 16},
		{value: "24", cursor: 2, limit: 3, digitsOnly: true},
	}, fields: [5]textField{
		{limit: 500},
		{value: "0", cursor: 1, limit: 19, digitsOnly: true},
		{value: deadline, cursor: len(deadline), limit: 16},
		{limit: api.MaxDescriptionCharacters, byteLimit: api.MaxDescriptionBytes, multiline: true},
		{limit: 4000, multiline: true},
	}}
}

func (f *textField) insert(text string) {
	if f.multiline {
		text = strings.ReplaceAll(strings.ReplaceAll(text, "\r\n", "\n"), "\r", "\n")
	}

	text = strings.Map(func(r rune) rune {
		if f.multiline && (r == '\n' || r == '\t') {
			return r
		}

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
	if len(runes)+len(added) > f.limit ||
		(f.byteLimit > 0 && len(f.value)+len(text) > f.byteLimit) {
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

	description := f.fields[3].value
	if f.descriptionSource == descriptionFromFile && f.descriptionFile == "" {
		return api.CreatePostPayload{}, errors.New("Choose a description file or select Write text.")
	}

	if strings.TrimSpace(description) == "" {
		return api.CreatePostPayload{}, errors.New("Add a description or load a text file.")
	}

	if len(description) > api.MaxDescriptionBytes ||
		utf8.RuneCountInString(description) > api.MaxDescriptionCharacters {
		return api.CreatePostPayload{}, errors.New(
			"Description must be at most 32 KiB and 12,000 characters.",
		)
	}

	funding, delivery, review, err := f.timingPayload(deadline)
	if err != nil {
		return api.CreatePostPayload{}, err
	}

	return api.CreatePostPayload{
		Title:                title,
		CostLamports:         cost,
		EndTime:              deadline.UTC(),
		Level:                levels[f.level],
		Description:          description,
		AcceptanceCriteria:   strings.TrimSpace(f.fields[4].value),
		InputFiles:           []string{},
		ExpectedOutputs:      []string{},
		FundingWindowSeconds: funding,
		DeliverBy:            delivery.UTC(),
		ReviewWindowSeconds:  review,
	}, nil
}

func (m model) updateForm(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	if m.form.timingOpen {
		return m.updateTimings(msg)
	}

	if m.form.importing {
		return m.updateDescriptionImport(msg)
	}

	fileDescription := m.form.focus == 3 && m.form.descriptionSource == descriptionFromFile
	if m.form.focus < len(m.form.fields) && m.form.fields[m.form.focus].multiline && !fileDescription &&
		m.form.fields[m.form.focus].multilineKey(msg, m.contentWidth()-1) {
		return m, nil
	}

	switch msg.String() {
	case "esc":
		return m.openPosts(m.own)
	case "ctrl+s":
		return m.submitPost()
	case "ctrl+o":
		m = m.openDescriptionImport()
		return m, m.searchDescriptionFiles()
	case "ctrl+d":
		m.form.timingOpen = true
		return m, nil
	case "tab", "down":
		m.form.focus = (m.form.focus + 1) % 9
	case "shift+tab", "up":
		m.form.focus = (m.form.focus + 8) % 9
	case "enter":
		if m.form.focus == 2 {
			return m.openDeadline(), nil
		}

		if m.form.focus == 6 {
			return m.submitPost()
		}

		if m.form.focus == 7 {
			return m.openPosts(m.own)
		}

		if m.form.focus == 8 {
			m.form.timingOpen = true
			return m, nil
		}

		if fileDescription {
			m = m.openDescriptionImport()
			return m, m.searchDescriptionFiles()
		}

		if m.form.focus < len(m.form.fields) && m.form.fields[m.form.focus].multiline {
			m.form.fields[m.form.focus].insert("\n")
			return m, nil
		}

		m.form.focus++
	default:
		if m.form.focus == 5 {
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
		} else if m.form.focus < len(m.form.fields) {
			if fileDescription {
				if msg.String() == "right" {
					m = m.editDescription()
				}

				return m, nil
			}

			m.form.fields[m.form.focus].key(msg)
		}
	}

	return m, nil
}

func (m model) editDescription() model {
	m.form.descriptionFile = ""
	m.form.descriptionSource = descriptionFromText
	m.form.focus = 3
	m.form.importing = false
	m.notice = ""

	return m
}

func (m model) submitPost() (tea.Model, tea.Cmd) {
	payload, err := m.form.payload()
	if err != nil {
		m.err = err
		return m, nil
	}

	m.loading, m.err, m.notice = true, nil, ""
	m.form.importing = false

	return m, m.createPost(payload)
}
