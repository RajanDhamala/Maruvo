package tui

import (
	"crypto/rand"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/rajandhamala/Maruvo/cli/internal/api"
)

type chatComposer struct {
	draft       textField
	attachments []localFile
	root        string
	suggestions []localFile
	selection   int
	completing  bool
	sequence    uint64
	fileError   string
	messageID   string
	messageText string
}

type localFilesFound struct {
	generation, sequence uint64
	files                []localFile
	err                  error
}

type chatSent struct {
	generation uint64
	uploaded   []api.WorkspaceFile
	err        error
}

func (c *chatComposer) paste(text string) {
	runes, added := []rune(c.draft.value), []rune(chatText(text))
	if len(runes)+len(added) > c.draft.limit {
		return
	}

	c.draft.value = string(runes[:c.draft.cursor]) + string(added) + string(runes[c.draft.cursor:])
	c.draft.cursor += len(added)
}

func (m model) composing() bool {
	return m.screen == workspaceScreen && !m.workspaceFiles && !m.workspaceReview &&
		m.workspaceAction == "" && !m.reviewConfirm
}

func (m *model) completeFiles() tea.Cmd {
	m.composer.sequence++
	m.composer.selection, m.composer.suggestions, m.composer.fileError = 0, nil, ""
	query, _, _, ok := attachmentToken(m.composer.draft)

	m.composer.completing = ok
	if !ok {
		return nil
	}

	root, sequence, generation := m.composer.root, m.composer.sequence, m.workspaceGen

	return func() tea.Msg {
		files, err := localFileSuggestions(root, query)
		return localFilesFound{generation: generation, sequence: sequence, files: files, err: err}
	}
}

func (m model) attachSuggestion() (tea.Model, tea.Cmd) {
	c := &m.composer
	if len(c.suggestions) == 0 {
		return m, nil
	}

	file := c.suggestions[min(c.selection, len(c.suggestions)-1)]

	_, start, end, ok := attachmentToken(c.draft)
	if !ok {
		return m, nil
	}

	if !file.directory {
		info, err := os.Stat(file.path)
		if err != nil {
			m.err = err
			return m, nil
		}

		if !info.Mode().IsRegular() || info.Size() <= 0 || info.Size() > 10<<20 {
			m.err = errors.New("Choose a file between 1 byte and 10 MiB.")
			return m, nil
		}

		for _, attached := range c.attachments {
			if attached.path == file.path {
				m.err = errors.New("That file is already attached.")
				return m, nil
			}
		}

		if len(c.attachments) >= 20 {
			m.err = errors.New("Send these attachments before adding more.")
			return m, nil
		}

		c.attachments = append(c.attachments, file)
	}

	runes := []rune(c.draft.value)

	replacement := ""
	if file.directory {
		replacement = "@" + file.label + string(filepath.Separator)
	}

	c.draft.value = string(runes[:start]) + replacement + string(runes[end:])
	c.draft.cursor = start + len([]rune(replacement))
	m.err, m.notice = nil, ""

	return m, m.completeFiles()
}

func (m model) startAttachment() (tea.Model, tea.Cmd) {
	m.workspaceFiles, m.workspaceReview = false, false

	_, _, _, active := attachmentToken(m.composer.draft)
	if !active {
		c := &m.composer.draft
		prefix := "@"

		runes := []rune(c.value)
		if c.cursor > 0 && !unicode.IsSpace(runes[c.cursor-1]) {
			prefix = " @"
		}

		c.insert(prefix)
	}

	return m, m.completeFiles()
}

func (m model) updateComposer(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	key := msg.String()

	c := &m.composer
	if c.completing {
		switch key {
		case "up":
			c.selection = max(0, c.selection-1)
			return m, nil
		case "down":
			c.selection = min(max(0, len(c.suggestions)-1), c.selection+1)
			return m, nil
		case "enter", "tab":
			return m.attachSuggestion()
		case "esc":
			c.completing, c.suggestions = false, nil
			c.sequence++

			return m, nil
		}
	}

	switch key {
	case "enter":
		return m.sendChat()
	case "shift+enter", "alt+enter":
		if len([]rune(c.draft.value)) < c.draft.limit {
			runes := []rune(c.draft.value)
			c.draft.value = string(runes[:c.draft.cursor]) + "\n" + string(runes[c.draft.cursor:])
			c.draft.cursor++
		}
	case "esc":
		return m.openDetail()
	case "ctrl+f":
		m.workspaceFiles, m.activityScroll = true, 0
		return m, nil
	case "ctrl+r":
		m.workspaceReview, m.activityScroll = true, 0
		return m, nil
	case "ctrl+s":
		return m.workspaceCommand("s")
	case "ctrl+backspace":
		if len(c.attachments) > 0 {
			c.attachments = c.attachments[:len(c.attachments)-1]
		}

		return m, nil
	case "pgup", "up":
		m.activityScroll += 3
		return m, nil
	case "pgdown", "down":
		m.activityScroll = max(0, m.activityScroll-3)
		return m, nil
	default:
		c.draft.key(msg)
	}

	m.err, m.notice = nil, ""

	return m, m.completeFiles()
}

func (m model) sendChat() (tea.Model, tea.Cmd) {
	text := strings.TrimSpace(m.composer.draft.value)

	commands := map[string]string{
		"/files":   "tab",
		"/review":  "v",
		"/task":    "b",
		"/submit":  "s",
		"/refresh": "r",
	}
	if action, ok := commands[text]; ok {
		if len(m.composer.attachments) > 0 {
			m.err = errors.New("Send or remove your attachments before using a command.")
			return m, nil
		}

		m.composer.draft = textField{limit: 4000}
		m.composer.messageID, m.composer.messageText = "", ""

		return m.workspaceCommand(action)
	}

	if text == "" && len(m.composer.attachments) == 0 {
		return m, nil
	}

	if m.workspace.Post.Status == "completed" || m.workspace.Post.Status == "cancelled" {
		m.err = errors.New("This workspace is closed.")
		return m, nil
	}

	files := append([]localFile(nil), m.composer.attachments...)

	generation, ctx, postID := m.workspaceGen, m.workspaceCtx, m.workspace.Post.ID
	if text != "" && (m.composer.messageID == "" || m.composer.messageText != text) {
		m.composer.messageID, m.composer.messageText = rand.Text(), text
	}

	messageID := m.composer.messageID

	purpose := "shared"
	if m.isPoster(m.workspace.Post) {
		purpose = "input"
	} else if m.isWorker(m.workspace.Post) && m.workspace.Escrow.State == "confirmed" {
		purpose = "output"
	}

	m.loading, m.err, m.notice, m.activityScroll = true, nil, "", 0

	return m, func() tea.Msg {
		result := chatSent{generation: generation}

		for _, file := range files {
			uploaded, err := m.client.UploadTaskFile(ctx, m.token, postID, file.path, purpose)
			if err != nil {
				result.err = fmt.Errorf("Send %s: %w", file.label, err)
				return result
			}

			result.uploaded = append(result.uploaded, uploaded)
		}

		if text != "" {
			result.err = m.client.SendMessage(ctx, m.token, postID, text, messageID)
		}

		return result
	}
}

func (m model) chatSent(msg chatSent) (tea.Model, tea.Cmd) {
	if m.screen != workspaceScreen || msg.generation != m.workspaceGen {
		return m, nil
	}

	m.loading = false

	m.composer.attachments = m.composer.attachments[min(len(msg.uploaded), len(m.composer.attachments)):]
	for _, uploaded := range msg.uploaded {
		found := false

		for _, existing := range m.workspace.Files {
			if uploaded.ID == existing.ID {
				found = true
				break
			}
		}

		if !found {
			m.workspace.Files = append(m.workspace.Files, uploaded)
		}
	}

	m.setPostError(msg.err)

	if msg.err == nil {
		m.composer.draft = textField{limit: 4000}
		m.composer.messageID, m.composer.messageText = "", ""
		m.notice = "Sent."
	}

	return m, nil
}

func (m model) composerRows(width int) []string {
	c := m.composer
	rows := []string{muted(strings.Repeat("─", width))}

	if len(c.attachments) > 0 {
		var names []string
		for _, file := range c.attachments {
			names = append(names, "["+plain(filepath.Base(file.path))+" ×]")
		}

		rows = append(rows, accent("  "+strings.Join(names, " ")))
	}

	input := muted(
		ansi.Truncate("Message · @filename to attach · /files /review /task", max(1, width-3), "…"),
	)
	runes := []rune(c.draft.value)

	cursor := min(c.draft.cursor, len(runes))
	if c.draft.value != "" {
		char := " "
		if cursor < len(runes) && runes[cursor] != '\n' {
			char = string(runes[cursor])
		}

		tail := cursor
		if cursor < len(runes) && runes[cursor] != '\n' {
			tail++
		}

		input = string(runes[:cursor]) + "\x1b[7m" + char + "\x1b[0m" + string(runes[tail:])
	} else {
		input = "\x1b[7m \x1b[0m" + input
	}

	lines := strings.Split(ansi.Wrap(input, max(1, width-2), ""), "\n")

	maxLines := m.bodyHeight() - 4
	if len(c.attachments) > 0 {
		maxLines--
	}

	if c.completing {
		maxLines -= 2
	}

	maxLines = max(1, min(3, maxLines))
	cursorLine := strings.Count(ansi.Wrap(string(runes[:cursor]), max(1, width-2), ""), "\n")

	start := max(0, min(cursorLine-maxLines+1, len(lines)-maxLines))
	for i, line := range lines[start:min(len(lines), start+maxLines)] {
		prefix := "  "
		if i == 0 {
			prefix = accent("› ")
		}

		rows = append(rows, prefix+line)
	}

	return append(rows, muted(strings.Repeat("─", width)))
}
