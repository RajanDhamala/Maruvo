package tui

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/rajandhamala/Maruvo/cli/internal/providers"
)

type localSessionsState struct {
	open, busy                    bool
	query                         textField
	selection                     int
	sequence                      uint64
	items                         []providers.ConversationSummary
	err                           error
	allFolders, archived, created bool
	focus                         int
}

func (m model) localConversationStore() providers.ConversationStore {
	return providers.ConversationStore{Scope: m.localAgent.chatScope, Client: m.client, Token: m.token}
}

type localSessionsLoaded struct {
	sequence, agentSequence uint64
	items                   []providers.ConversationSummary
	err                     error
}

type localConversationLoaded struct {
	sequence, agentSequence uint64
	chat                    providers.Conversation
	err                     error
}

type localConversationSaved struct {
	id      string
	scope   providers.ChatScope
	updated time.Time
	err     error
}

func (m *model) saveLocalConversation() tea.Cmd {
	a := &m.localAgent
	if a.chat.ID == "" {
		return nil
	}

	a.chat.UpdatedAt = time.Now().UTC()
	a.chat.Lines = append([]string{}, a.lines...)
	a.chat.Draft, a.chat.Usage = a.input.value, a.usage
	chat, scope, store := a.chat, a.chatScope, m.localConversationStore()
	chat.Messages = append([]providers.ChatMessage{}, chat.Messages...)
	safe := func(value string) string {
		value = a.client.SafeChatText(value, a.marketplace)
		if m.token != "" {
			value = strings.ReplaceAll(value, m.token, "[Maruvo token redacted]")
		}

		return value
	}
	chat.Title = safe(chat.Title)

	chat.Draft = safe(chat.Draft)
	for i := range chat.Messages {
		chat.Messages[i].Content = safe(chat.Messages[i].Content)
	}

	for i := range chat.Lines {
		chat.Lines[i] = safe(chat.Lines[i])
	}

	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		return localConversationSaved{id: chat.ID, scope: scope, updated: chat.UpdatedAt,
			err: store.Save(ctx, chat)}
	}
}

func (m model) openLocalSessions() (tea.Model, tea.Cmd) {
	if m.localAgent.busy && m.localAgent.client != nil {
		return m, nil
	}

	previousSave := m.saveLocalConversation()
	m.localAgent.files = agentFilePicker{sequence: m.localAgent.files.sequence + 1}
	s := &m.localAgent.sessions
	*s = localSessionsState{
		open:     true,
		busy:     true,
		focus:    3,
		query:    textField{limit: 200},
		sequence: s.sequence + 1,
	}
	sequence, agentSequence, store := s.sequence, m.localAgent.sequence, m.localConversationStore()
	load := func() tea.Msg {
		var saveErr error
		if previousSave != nil {
			saveErr = previousSave().(localConversationSaved).err
		}

		ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
		defer cancel()

		items, err := store.List(ctx)
		if saveErr != nil {
			err = saveErr
		}

		return localSessionsLoaded{sequence: sequence, agentSequence: agentSequence, items: items, err: err}
	}

	return m, load
}

func (m model) newLocalConversation() (tea.Model, tea.Cmd) {
	cmd := m.saveLocalConversation()
	a := &m.localAgent
	a.chat = providers.Conversation{}
	a.history, a.lines, a.toolRows = nil, nil, nil
	a.files, a.render = agentFilePicker{sequence: a.files.sequence + 1}, &localAgentRender{}
	a.streaming, a.thinking, a.thinkStarted, a.thinkTime = false, "", time.Time{}, 0

	a.usage, a.status, a.storageErr, a.scroll = "", "New conversation.", nil, 0
	if a.client != nil {
		a.err = nil
	}

	a.input = textField{limit: 12000, byteLimit: 16000, multiline: true}
	a.sessions.open, a.sessions.sequence = false, a.sessions.sequence+1

	return m, cmd
}

func (m model) sessionIndices() []int {
	query := strings.ToLower(strings.TrimSpace(m.localAgent.sessions.query.value))

	var indices []int

	for i, chat := range m.localAgent.sessions.items {
		if chat.Archived != m.localAgent.sessions.archived ||
			(!m.localAgent.sessions.allFolders && filepath.Clean(chat.Directory) != filepath.Clean(m.localDirectory())) {
			continue
		}

		if strings.Contains(
			strings.ToLower(chat.Title+" "+chat.Directory+" "+chat.Provider+" "+chat.Model),
			query,
		) {
			indices = append(indices, i)
		}
	}

	sort.SliceStable(indices, func(i, j int) bool {
		a, b := m.localAgent.sessions.items[indices[i]], m.localAgent.sessions.items[indices[j]]
		if m.localAgent.sessions.created {
			return a.CreatedAt.After(b.CreatedAt)
		}

		return a.UpdatedAt.After(b.UpdatedAt)
	})

	return indices
}

func (m model) resumeLocalConversation(index int) (tea.Model, tea.Cmd) {
	indices := m.sessionIndices()
	if index < 0 || index >= len(indices) || m.localAgent.sessions.busy {
		return m, nil
	}

	s := &m.localAgent.sessions
	s.busy, s.err, s.sequence = true, nil, s.sequence+1
	selected, store, sequence := s.items[indices[index]], m.localConversationStore(), s.sequence
	agentSequence := m.localAgent.sequence

	return m, func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		chat, err := store.Load(ctx, selected.Directory, selected.ID)

		return localConversationLoaded{sequence: sequence, agentSequence: agentSequence, chat: chat, err: err}
	}
}

func (m model) updateLocalSessions(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	s := &m.localAgent.sessions

	switch msg.String() {
	case "esc", "ctrl+r", "ctrl+h":
		s.open, s.sequence = false, s.sequence+1
	case "ctrl+n":
		return m.newLocalConversation()
	case "ctrl+d":
		return m.archiveLocalSession()
	case "ctrl+f":
		s.allFolders, s.selection = !s.allFolders, 0
	case "ctrl+s":
		s.archived, s.selection = !s.archived, 0
	case "ctrl+o":
		s.created, s.selection = !s.created, 0
	case "tab":
		s.focus = (s.focus + 1) % 4
	case "shift+tab":
		s.focus = (s.focus + 3) % 4
	case "left", "right":
		switch s.focus {
		case 0:
			s.allFolders, s.selection = !s.allFolders, 0
		case 1:
			s.archived, s.selection = !s.archived, 0
		case 2:
			s.created, s.selection = !s.created, 0
		default:
			s.query.key(msg)
		}
	case "up":
		s.selection = max(0, s.selection-1)
	case "down":
		s.selection = min(max(0, len(m.sessionIndices())-1), s.selection+1)
	case "enter":
		return m.resumeLocalConversation(s.selection)
	case "pgup":
		s.selection = max(0, s.selection-max(1, m.height-10))
	case "pgdown":
		s.selection = min(max(0, len(m.sessionIndices())-1), s.selection+max(1, m.height-10))
	default:
		if !s.busy {
			s.focus = 3
			s.query.key(msg)
			s.selection = 0
		}
	}

	return m, nil
}

func (m model) archiveLocalSession() (tea.Model, tea.Cmd) {
	s := &m.localAgent.sessions

	indices := m.sessionIndices()
	if s.busy || s.selection >= len(indices) {
		return m, nil
	}

	selected, store := s.items[indices[s.selection]], m.localConversationStore()
	if selected.ID == m.localAgent.chat.ID {
		m.localAgent.chat.Archived = !selected.Archived
	}

	s.busy, s.sequence = true, s.sequence+1
	sequence, agentSequence := s.sequence, m.localAgent.sequence
	previous := append([]providers.ConversationSummary{}, s.items...)

	return m, func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
		defer cancel()

		chat, err := store.Load(ctx, selected.Directory, selected.ID)
		if err != nil {
			return localSessionsLoaded{
				sequence:      sequence,
				agentSequence: agentSequence,
				items:         previous,
				err:           err,
			}
		}

		chat.Archived, chat.UpdatedAt = !selected.Archived, time.Now().UTC()
		saveErr := store.Save(ctx, chat)
		items, err := store.List(ctx)

		return localSessionsLoaded{
			sequence:      sequence,
			agentSequence: agentSequence,
			items:         items,
			err:           errors.Join(saveErr, err),
		}
	}
}

func sessionAge(when, now time.Time) string {
	d := max(time.Duration(0), now.Sub(when))
	switch {
	case d < time.Minute:
		return "now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d/time.Minute))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d/time.Hour))
	default:
		return fmt.Sprintf("%dd ago", int(d/(24*time.Hour)))
	}
}

func sessionChoice(label string, selected, focused bool) string {
	if selected {
		style := "\x1b[1;37;48;2;64;64;64m"
		if focused {
			style = "\x1b[1;4;37;48;2;64;64;64m"
		}

		return style + " " + label + " \x1b[0m"
	}

	return muted(" " + label + " ")
}

func (m model) localSessionsLayout() postLayout {
	width, height := m.dimensions()
	inside := width - 4
	s := m.localAgent.sessions
	l := postLayout{footer: m.shortcutHint()}
	groups := []struct {
		label, action string
		choices       [2]string
		selected      bool
	}{
		{"Filter: ", "session-folder", [2]string{"Cwd", "All"}, s.allFolders},
		{"Status: ", "session-status", [2]string{"Active", "Archived"}, s.archived},
		{"Sort: ", "session-sort", [2]string{"Updated", "Created"}, s.created},
	}

	var toolbar string

	for group, control := range groups {
		row := muted(control.label)
		for choice, label := range control.choices {
			row += sessionChoice(label, control.selected == (choice == 1), s.focus == group)
		}

		if toolbar != "" && ansi.StringWidth(toolbar)+3+ansi.StringWidth(row) > inside {
			l.rows = append(l.rows, toolbar)
			toolbar = ""
		}

		if toolbar != "" {
			toolbar += "   "
		}

		x := ansi.StringWidth(toolbar) + len(control.label)
		for choice, label := range control.choices {
			l.hit(x, len(l.rows), len(label)+2, 1, control.action, choice)
			x += len(label) + 2
		}

		toolbar += row
	}

	l.rows = append(l.rows, toolbar, "")

	searchRow := len(l.rows)
	if s.query.value == "" {
		l.rows = append(l.rows, muted("Type to search"))
	} else {
		l.rows = append(l.rows, "Search: "+s.query.render(inside-8, !s.busy && s.focus == 3))
	}

	l.hit(0, searchRow, inside, 1, "session-search", 0)
	l.rows = append(l.rows, "")

	indices := m.sessionIndices()
	visible := max(1, height-7-len(l.rows))

	start := max(0, s.selection-visible+1)
	now := time.Now()

	for row := start; row < min(len(indices), start+visible); row++ {
		chat := s.items[indices[row]]

		when := chat.UpdatedAt
		if s.created {
			when = chat.CreatedAt
		}

		marker := "  "
		if row == s.selection {
			marker = "› "
		}

		label := fmt.Sprintf("%s%-12s%s", marker, sessionAge(when, now), plain(chat.Title))
		label = ansi.Truncate(label, inside, "…")
		label += strings.Repeat(" ", max(0, inside-ansi.StringWidth(label)))

		style := "\x1b[38;2;220;220;220m"
		if row%2 == 1 {
			style += "\x1b[48;2;32;32;32m"
		}

		if row == s.selection {
			style = "\x1b[1;38;2;10;10;10;48;2;96;165;250m"
		}

		l.hit(0, len(l.rows), inside, 1, "session-open", row)
		l.rows = append(l.rows, style+label+"\x1b[0m")
	}

	if s.busy {
		l.rows = append(l.rows, muted("Loading chats…"))
	} else if s.err != nil {
		l.rows = append(l.rows, warning(plain(s.err.Error())))
	} else if len(indices) == 0 {
		l.rows = append(l.rows, muted("No saved chats match these filters."))
		if len(s.items) == 0 {
			l.rows = append(l.rows, muted("Chats from earlier builds were not saved."))
		}
	}

	if len(indices) > 0 && s.selection < len(indices) {
		l.rows = append(
			l.rows,
			muted("Folder: "+plain(displayHomePath(s.items[indices[s.selection]].Directory))),
		)
	}

	return l
}

func (m model) updateLocalSessionMouse(msg tea.MouseClickMsg) (tea.Model, tea.Cmd) {
	if msg.Button != tea.MouseLeft {
		return m, nil
	}

	for _, hit := range m.localSessionsLayout().hits {
		if msg.X < 2+hit.x || msg.X >= 2+hit.x+hit.width || msg.Y < 2+hit.y || msg.Y >= 2+hit.y+hit.height {
			continue
		}

		s := &m.localAgent.sessions

		switch hit.action {
		case "session-folder":
			s.allFolders, s.selection, s.focus = hit.index == 1, 0, 0
		case "session-status":
			s.archived, s.selection, s.focus = hit.index == 1, 0, 1
		case "session-sort":
			s.created, s.selection, s.focus = hit.index == 1, 0, 2
		case "session-search":
			s.focus = 3
		default:
			return m.resumeLocalConversation(hit.index)
		}

		return m, nil
	}

	return m, nil
}
