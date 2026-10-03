package tui

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"unicode"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/rajandhamala/Maruvo/cli/internal/api"
)

type descriptionFileSearch struct {
	root        string
	suggestions []localFile
	selection   int
	searching   bool
	err         error
}

type descriptionFilesFound struct {
	sequence uint64
	files    []localFile
	err      error
}

type descriptionLoaded struct {
	text string
	name string
	err  error
}

func readDescription(path string) (string, error) {
	path = strings.TrimSpace(path)
	if strings.HasPrefix(path, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}

		path = filepath.Join(home, path[2:])
	}

	if !filepath.IsAbs(path) {
		path = filepath.Join(projectDirectory(), path)
	}

	info, err := os.Stat(path)
	if err != nil {
		return "", err
	}

	if !info.Mode().IsRegular() || info.Size() > api.MaxDescriptionBytes {
		return "", errors.New("Choose a text file no larger than 32 KiB.")
	}

	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()

	data, err := io.ReadAll(io.LimitReader(file, api.MaxDescriptionBytes+1))
	if err != nil {
		return "", err
	}

	if len(data) > api.MaxDescriptionBytes || utf8.RuneCount(data) > api.MaxDescriptionCharacters {
		return "", errors.New("Description must be at most 32 KiB and 12,000 characters.")
	}

	if !utf8.Valid(data) || strings.IndexFunc(string(data), func(r rune) bool {
		return unicode.IsControl(r) && r != '\n' && r != '\r' && r != '\t'
	}) >= 0 {
		return "", errors.New("Choose a UTF-8 text file, such as .txt, .md, or source code.")
	}

	text := strings.ReplaceAll(strings.ReplaceAll(string(data), "\r\n", "\n"), "\r", "\n")
	if strings.TrimSpace(text) == "" {
		return "", errors.New("The description file is empty.")
	}

	return text, nil
}

func (m model) openDescriptionImport() model {
	m.form.importing, m.form.focus = true, 3
	m.form.descriptionSource = descriptionFromFile
	m.form.path = textField{limit: 4096}
	m.form.fileSearch = descriptionFileSearch{root: projectDirectory()}
	m.err, m.notice = nil, ""

	return m
}

func descriptionFileSuggestions(root, query string) ([]localFile, error) {
	query = strings.TrimSpace(query)
	if strings.HasPrefix(query, "/") && strings.Count(query, "/") == 1 {
		query = query[1:]
	}

	clean := filepath.Clean(query)
	if filepath.IsAbs(query) || strings.HasPrefix(query, "~") ||
		clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return nil, errors.New("Search for a project file, such as /readme.")
	}

	return localFileSuggestions(root, query)
}

func (m *model) searchDescriptionFiles() tea.Cmd {
	m.descriptionSearchSeq++
	m.form.fileSearch.suggestions, m.form.fileSearch.selection = nil, 0
	m.form.fileSearch.searching, m.form.fileSearch.err = true, nil
	m.err = nil
	sequence, root, query := m.descriptionSearchSeq, m.form.fileSearch.root, m.form.path.value

	return func() tea.Msg {
		files, err := descriptionFileSuggestions(root, query)
		return descriptionFilesFound{sequence: sequence, files: files, err: err}
	}
}

func (m model) selectDescriptionFile(load bool) (tea.Model, tea.Cmd) {
	search := &m.form.fileSearch
	if search.searching {
		return m, nil
	}

	if len(search.suggestions) == 0 {
		return m, nil
	}

	file := search.suggestions[search.selection]

	m.form.path.value = file.label
	if file.directory {
		m.form.path.value += string(filepath.Separator)
	}

	m.form.path.cursor = utf8.RuneCountInString(m.form.path.value)
	if file.directory || !load {
		return m, m.searchDescriptionFiles()
	}

	return m.loadDescription(file.path)
}

func (m model) loadDescription(path string) (tea.Model, tea.Cmd) {
	m.loading, m.err = true, nil

	return m, func() tea.Msg {
		text, err := readDescription(path)
		return descriptionLoaded{text: text, name: filepath.Base(path), err: err}
	}
}

func (m model) updateDescriptionImport(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.form.importing, m.err = false, nil
	case "enter":
		return m.selectDescriptionFile(true)
	case "tab":
		return m.selectDescriptionFile(false)
	case "ctrl+s":
		return m.submitPost()
	case "up":
		m.form.fileSearch.selection = max(0, m.form.fileSearch.selection-1)
	case "down":
		search := &m.form.fileSearch
		search.selection = min(max(0, len(search.suggestions)-1), search.selection+1)
	default:
		before := m.form.path.value
		m.form.path.key(msg)

		if m.form.path.value != before {
			return m, m.searchDescriptionFiles()
		}
	}

	return m, nil
}

func (m model) descriptionSearchRows(l *postLayout) int {
	width := m.contentWidth()

	label := "Project files"
	l.rows = append(l.rows, accent(label))

	y := len(l.rows)

	rows, _ := formFieldRows(m.form.path, width, 1, true, "Search project files · /readme")
	if m.bodyHeight() < 11 {
		rows = rows[1:2]
	}

	l.rows = append(l.rows, rows...)
	l.hit(0, y, width, len(rows), "description-import", 0)

	l.footer = "↑↓ select · Enter load · Tab complete · Esc close"

	search := m.form.fileSearch
	if search.searching || search.err != nil || len(search.suggestions) == 0 {
		message := "No matching files. Try another filename."
		if search.searching {
			message = "Searching files…"
		} else if search.err != nil {
			message = search.err.Error()
		}

		l.rows = append(l.rows, muted(ansi.Truncate(plain(message), width, "…")))

		return len(l.rows) - 1
	}

	visible := max(1, min(6, m.bodyHeight()-4-len(rows)))

	start := max(0, search.selection-visible+1)
	for i := start; i < min(len(search.suggestions), start+visible); i++ {
		file := search.suggestions[i]

		label := "  " + plain(file.label)
		if file.directory {
			label += string(filepath.Separator)
		}

		label = ansi.Truncate(label, width, "…")
		if i == search.selection {
			label = accent("› " + strings.TrimPrefix(label, "  "))
		}

		l.hit(0, len(l.rows), width, 1, "description-file", i)
		l.rows = append(l.rows, label)
	}

	if m.loading {
		l.footer = "Reading description…"
	}

	return len(l.rows) - 1
}

func (m model) descriptionLoaded(msg descriptionLoaded) (tea.Model, tea.Cmd) {
	if m.screen != newPostScreen || !m.form.importing {
		return m, nil
	}

	m.loading, m.err = false, msg.err
	if msg.err == nil {
		m.form.fields[3].value, m.form.fields[3].cursor = msg.text, utf8.RuneCountInString(msg.text)
		m.form.descriptionFile = msg.name
		m.form.importing = false
		m.form.path = textField{limit: 4096}
		m.notice = ""
	}

	return m, nil
}
