package tui

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/rajandhamala/Maruvo/cli/internal/providers"
)

type agentFilePicker struct {
	open, loading bool
	items         []localFile
	selection     int
	sequence      uint64
	err           error
}

type agentFilesFound struct {
	agentSequence, sequence uint64
	files                   []localFile
	err                     error
}

func agentReferenceToken(field textField) (string, int, int, bool) {
	runes := []rune(field.value)
	cursor := min(field.cursor, len(runes))

	before := string(runes[:cursor])
	if at := strings.LastIndex(before, "@\""); at >= 0 {
		start := utf8.RuneCountInString(before[:at])
		if start == 0 || unicode.IsSpace(runes[start-1]) {
			escaped := false
			for i := start + 2; i < cursor; i++ {
				if !escaped && runes[i] == '"' {
					return attachmentToken(field)
				}

				if !escaped && runes[i] == '\\' {
					escaped = true
				} else {
					escaped = false
				}
			}

			query := string(runes[start+2 : cursor])

			var decoded string
			if json.Unmarshal([]byte("\""+query+"\""), &decoded) == nil {
				query = decoded
			}

			end := cursor
			for end < len(runes) && runes[end] != '"' && runes[end] != '\n' {
				end++
			}

			if end < len(runes) && runes[end] == '"' {
				end++
			}

			return query, start, end, true
		}
	}

	return attachmentToken(field)
}

func agentFileSuggestions(directory, query string) ([]localFile, error) {
	if filepath.IsAbs(query) || strings.HasPrefix(query, "~") || strings.Contains(query, "\\") {
		return nil, errors.New("Choose a file inside this project.")
	}

	for _, part := range strings.Split(filepath.ToSlash(query), "/") {
		if part == ".." {
			return nil, errors.New("Choose a file inside this project.")
		}
	}

	root, err := os.OpenRoot(directory)
	if err != nil {
		return nil, err
	}
	defer root.Close()

	parent, _ := filepath.Split(query)
	if !providers.ProjectPathAllowed(root, filepath.ToSlash(filepath.Clean(parent))) {
		return nil, errors.New("Choose a project folder outside credentials and symlinks.")
	}

	files, err := localFileSuggestions(directory, query)
	if err != nil {
		return nil, err
	}

	var allowed []localFile

	for _, file := range files {
		if providers.ProjectPathAllowed(root, filepath.ToSlash(file.label)) {
			allowed = append(allowed, file)
		}
	}

	if query != "" && !strings.Contains(query, string(filepath.Separator)) {
		entries, err := os.ReadDir(directory)
		if err != nil {
			return nil, err
		}

		for _, entry := range entries {
			if entry.IsDir() && strings.Contains(strings.ToLower(entry.Name()), strings.ToLower(query)) &&
				providers.ProjectPathAllowed(root, entry.Name()) {
				allowed = append(allowed, localFile{path: filepath.Join(directory, entry.Name()),
					label: entry.Name(), directory: true})
			}
		}

		sort.SliceStable(allowed, func(i, j int) bool { return allowed[i].label < allowed[j].label })
	}

	return allowed[:min(12, len(allowed))], nil
}

func (m *model) completeAgentInput() tea.Cmd {
	a := &m.localAgent
	if a.busy || a.approval != nil {
		return nil
	}

	if strings.HasPrefix(a.input.value, "/") {
		query := a.input
		*m = m.openCommands()
		m.commands.agent, m.commands.query = true, query
		a.input = textField{limit: 12000, byteLimit: 16000, multiline: true}
	}

	a.files = agentFilePicker{sequence: a.files.sequence + 1}
	query, _, _, active := agentReferenceToken(a.input)

	a.files.open, a.files.loading = active, active
	if !active {
		return nil
	}

	agentSequence, sequence, directory := a.sequence, a.files.sequence, m.localDirectory()

	return func() tea.Msg {
		files, err := agentFileSuggestions(directory, query)
		return agentFilesFound{agentSequence: agentSequence, sequence: sequence, files: files, err: err}
	}
}

func (m model) selectAgentFile() (tea.Model, tea.Cmd) {
	a := &m.localAgent
	if a.busy || a.files.loading || len(a.files.items) == 0 {
		return m, nil
	}

	_, start, end, active := agentReferenceToken(a.input)
	if !active {
		return m, nil
	}

	file := a.files.items[min(a.files.selection, len(a.files.items)-1)]

	replacement := "@" + file.label
	if file.directory {
		replacement += "/"
	}

	if strings.ContainsAny(file.label, " \t\"") {
		quoted, _ := json.Marshal(strings.TrimPrefix(replacement, "@"))

		replacement = "@" + string(quoted)
		if file.directory {
			replacement = strings.TrimSuffix(replacement, "\"")
		}
	}

	if !file.directory {
		replacement += " "
	}

	runes := []rune(a.input.value)

	value := string(runes[:start]) + replacement + string(runes[end:])
	if utf8.RuneCountInString(value) > a.input.limit ||
		(a.input.byteLimit > 0 && len(value) > a.input.byteLimit) {
		a.files.err = errors.New("This file reference exceeds the prompt limit.")
		return m, nil
	}

	a.input.value, a.input.cursor = value, start+utf8.RuneCountInString(replacement)

	return m, m.completeAgentInput()
}

func (m model) agentFileRows(width, available int) ([]string, []hitArea) {
	picker := m.localAgent.files
	if !picker.open || available < 1 {
		return nil, nil
	}

	visible := min(6, available)

	var (
		rows []string
		hits []hitArea
	)

	put := func(text string) { rows = append(rows, inputRow(text, width)) }
	if visible > 1 {
		put(muted("Project files · ↑↓ select · Tab / Enter insert"))

		visible--
	}

	if picker.err != nil || picker.loading || len(picker.items) == 0 {
		text := "No matching project files."
		if picker.err != nil {
			text = picker.err.Error()
		} else if picker.loading {
			text = "Searching project files…"
		}

		put(muted(plain(text)))

		return rows, hits
	}

	start := max(0, picker.selection-visible+1)
	for i := start; i < min(len(picker.items), start+visible); i++ {
		name := picker.items[i].label
		if picker.items[i].directory {
			name += "/"
		}

		text := "  " + plain(name)
		if i == picker.selection {
			text = accent("› " + plain(name))
		}

		hits = append(hits, hitArea{y: len(rows), width: width, height: 1, action: "agent-file", index: i})
		put(ansi.Truncate(text, width-2, "…"))
	}

	return rows, hits
}
