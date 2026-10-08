package tui

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

var slashCommands = []struct {
	name, label string
}{
	{"/open", "Open directory"},
	{"/model", "Connect provider / choose model"},
	{"/agent", "Open local CLI agent"},
	{"/sessions", "Browse saved agent chats"},
	{"/remote", "Remote agents / queued work / harness setup"},
	{"/priority", "Priority · coming soon"},
	{"/new", "New task · coming soon"},
}

type commandMenu struct {
	open, directory, searching bool
	agent                      bool
	query                      textField
	root                       string
	selection                  int
	folders                    []localFile
	err                        error
	sequence                   uint64
}

type directoriesFound struct {
	sequence uint64
	folders  []localFile
	err      error
}

type directoryOpened struct {
	sequence uint64
	path     string
	err      error
}

func (m model) localDirectory() string {
	if m.directory != "" {
		return m.directory
	}

	return projectDirectory()
}

func (m model) canOpenCommands() bool {
	if m.demo || m.loading || m.profileOpen || m.picker.open || m.dashboard.filterOpen {
		return false
	}

	if m.token == "" {
		return m.homeFocus != homePrompt || m.homeInput.value == ""
	}

	return m.onDashboard() && (m.dashboard.focus == dashboardTasks ||
		(m.dashboard.focus == dashboardPrompt && m.homeInput.value == ""))
}

func (m model) openCommands() model {
	m.commands = commandMenu{
		open:     true,
		query:    textField{value: "/", cursor: 1, limit: 100},
		sequence: m.commands.sequence + 1,
	}

	return m
}

func (m model) commandIndices() []int {
	query := strings.ToLower(strings.TrimPrefix(strings.TrimSpace(m.commands.query.value), "/"))

	var indices []int

	for i, command := range slashCommands {
		if strings.Contains(strings.ToLower(command.name+" "+command.label), query) {
			indices = append(indices, i)
		}
	}

	return indices
}

func (m model) chooseCommand(index int) (tea.Model, tea.Cmd) {
	if m.commands.agent {
		if m.localAgent.busy {
			return m, nil
		}

		switch slashCommands[index].name {
		case "/agent":
			m.commands.open = false
			return m, nil
		case "/sessions":
			m.commands.open = false
			return m.openLocalSessions()
		case "/new":
			m.commands.open = false
			return m.newLocalConversation()
		}
	}

	if slashCommands[index].name == "/remote" {
		return m.openRemote()
	}

	if slashCommands[index].name == "/model" {
		return m.openProviders()
	}

	if slashCommands[index].name == "/agent" {
		return m.openLocalAgent()
	}

	if slashCommands[index].name == "/sessions" {
		next, ready := m.openLocalAgent()
		m = next.(model)
		next, history := m.openLocalSessions()

		return next, tea.Batch(ready, history)
	}

	if index != 0 {
		m.commands.err = errors.New("This command is coming soon.")
		return m, nil
	}

	path := m.directory
	if path == "" {
		path, _ = os.Getwd()
	}

	path += string(filepath.Separator)
	m.commands.directory = true
	m.commands.root = path
	m.commands.query = textField{value: path, cursor: utf8.RuneCountInString(path), limit: 4096}

	return m, m.findDirectories()
}

func directoryPath(base, value string) (string, error) {
	path := strings.TrimSpace(value)
	if path == "~" || strings.HasPrefix(path, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}

		path = filepath.Join(home, strings.TrimPrefix(path, "~"))
	}

	if !filepath.IsAbs(path) {
		path = filepath.Join(base, path)
	}

	return filepath.Abs(path)
}

func directorySuggestions(base, query string) ([]localFile, error) {
	path, err := directoryPath(base, query)
	if err != nil {
		return nil, err
	}

	directory, needle := filepath.Split(path)
	if query == "" || strings.HasSuffix(query, string(filepath.Separator)) || query == "~" {
		directory, needle = path, ""
	}

	directory = filepath.Clean(directory)

	entries, err := os.ReadDir(directory)
	if err != nil {
		return nil, err
	}

	parent := filepath.Dir(directory)

	var folders []localFile

	if needle == "" && parent != directory {
		folders = append(folders, localFile{path: parent, label: "..", directory: true})
	}

	for _, entry := range entries {
		if !strings.Contains(strings.ToLower(entry.Name()), strings.ToLower(needle)) ||
			(strings.HasPrefix(entry.Name(), ".") && !strings.HasPrefix(needle, ".")) {
			continue
		}

		path := filepath.Join(directory, entry.Name())
		info, err := os.Stat(path)

		if err == nil && info.IsDir() {
			folders = append(folders, localFile{path: path, label: entry.Name(), directory: true})
		}
	}

	return folders, nil
}

func (m *model) findDirectories() tea.Cmd {
	m.commands.sequence++
	m.commands.selection, m.commands.folders, m.commands.err = -1, nil, nil
	m.commands.searching = true
	sequence, base, query := m.commands.sequence, m.commands.root, m.commands.query.value

	return func() tea.Msg {
		folders, err := directorySuggestions(base, query)
		return directoriesFound{sequence: sequence, folders: folders, err: err}
	}
}

func (m model) openDirectory() (tea.Model, tea.Cmd) {
	query := m.commands.query.value
	if m.commands.selection >= 0 && m.commands.selection < len(m.commands.folders) {
		query = m.commands.folders[m.commands.selection].path
	}

	m.commands.sequence++
	sequence, base := m.commands.sequence, m.commands.root

	return m, func() tea.Msg {
		path, err := directoryPath(base, query)
		if err == nil {
			path, err = filepath.EvalSymlinks(path)
		}

		if err == nil {
			_, err = os.ReadDir(path)
		}

		return directoryOpened{sequence: sequence, path: path, err: err}
	}
}

func (m model) browseDirectory(index int) (tea.Model, tea.Cmd) {
	if index < 0 || index >= len(m.commands.folders) {
		return m, nil
	}

	path := m.commands.folders[index].path + string(filepath.Separator)
	m.commands.query.value, m.commands.query.cursor = path, utf8.RuneCountInString(path)

	return m, m.findDirectories()
}

func (m model) updateCommands(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	count := len(m.commandIndices())
	if m.commands.directory {
		count = len(m.commands.folders)
	}

	switch msg.String() {
	case "esc":
		m.commands.open = false
	case "up":
		m.commands.selection = max(0, m.commands.selection-1)
	case "down":
		m.commands.selection = min(max(0, count-1), m.commands.selection+1)
	case "enter":
		if m.commands.directory {
			return m.openDirectory()
		}

		indices := m.commandIndices()
		if len(indices) > 0 {
			return m.chooseCommand(indices[min(m.commands.selection, len(indices)-1)])
		}
	case "tab":
		if m.commands.directory {
			return m.browseDirectory(max(0, m.commands.selection))
		}
	default:
		before := m.commands.query.value
		m.commands.query.key(msg)

		if m.commands.query.value != before {
			return m.updateCommandQuery()
		}
	}

	return m, nil
}

func (m model) updateCommandQuery() (tea.Model, tea.Cmd) {
	m.commands.err, m.commands.selection = nil, 0
	if m.commands.directory {
		return m, m.findDirectories()
	}

	if !strings.HasPrefix(m.commands.query.value, "/") {
		m.commands.open = false
		if m.commands.agent {
			m.localAgent.input = m.commands.query
			return m, m.completeAgentInput()
		}

		if m.commands.query.value != "" {
			m.homeInput.value, m.homeInput.cursor = m.commands.query.value, m.commands.query.cursor
		}

		m.homeFocus, m.homeInput.limit = homePrompt, 2000
		m.dashboard.focus, m.dashboard.promptHidden = dashboardPrompt, false
	}

	return m, nil
}

func (m model) commandPromptArea() hitArea {
	width, height := m.dimensions()
	if m.commands.agent {
		inputHeight := 5
		if height < 22 {
			inputHeight = 3
		}

		return hitArea{x: 2, y: height - inputHeight - 3, width: width - 4, height: inputHeight}
	}

	if m.token != "" {
		return hitArea{x: m.contentX(), y: height - homePromptHeight - 3,
			width: m.dashboardGeometry().mainWidth, height: homePromptHeight}
	}

	paneWidth := min(104, width-4)

	return hitArea{x: (width - paneWidth) / 2, y: height - homePromptHeight - 4,
		width: paneWidth, height: homePromptHeight}
}

func (m model) commandLayout() postLayout {
	prompt := m.commandPromptArea()
	width := prompt.width
	l := postLayout{}

	available := max(1, prompt.y-2)
	if m.commands.agent {
		available = max(1, prompt.y-5)
	}

	put := func(text string) {
		l.rows = append(l.rows, dashboardPanelRow(ansi.Truncate(text, width-2, "…"), width))
	}

	mode, submit := "Commands", muted("Enter select")

	if m.commands.directory {
		mode, submit = "Open directory", muted("Enter open")
		put("  " + bold(mode))

		visible := min(5, available)
		if m.commands.agent {
			visible = max(1, visible-2)
		}

		if m.commands.err != nil {
			visible--
		}

		start := max(0, m.commands.selection-visible+1)

		for i := start; i < min(len(m.commands.folders), start+visible); i++ {
			label := "  " + plain(m.commands.folders[i].label) + string(filepath.Separator)
			if i == m.commands.selection {
				label = accent("› " + strings.TrimPrefix(label, "  "))
			}

			l.hit(0, len(l.rows), width, 1, "directory", i)
			put(label)
		}

		if len(m.commands.folders) == 0 {
			text := "No matching folders. Enter opens the typed path."
			if m.commands.searching {
				text = "Reading folders…"
			}

			put(muted(text))
		}
	} else {
		nameWidth := 0
		for _, command := range slashCommands {
			nameWidth = max(nameWidth, ansi.StringWidth(command.name))
		}

		indices := m.commandIndices()

		visible := max(1, min(len(indices), available))
		if m.commands.err != nil {
			visible = max(1, visible-1)
		}

		start := max(0, m.commands.selection-visible+1)
		for row := start; row < min(len(indices), start+visible); row++ {
			i := indices[row]

			command := slashCommands[i]
			if m.commands.agent && command.name == "/new" {
				command.label = "Start a new chat"
			}

			label := "  " + command.name + strings.Repeat(" ", nameWidth-len(command.name)+2) + command.label

			if row == m.commands.selection {
				label = accent("› " + strings.TrimPrefix(label, "  "))
			} else {
				label = muted(label)
			}

			l.hit(0, len(l.rows), width, 1, "command", i)
			put(label)
		}

		if len(m.commandIndices()) == 0 {
			put(muted("No matching commands."))
		}
	}

	if m.commands.err != nil {
		put("  " + warning(plain(m.commands.err.Error())))
	}

	if m.commands.directory {
		open, cancel := button(" Open directory ", false), button(" Cancel ", false)
		l.hit(2, len(l.rows), ansi.StringWidth(open), 1, "directory-open", 0)
		l.hit(ansi.StringWidth(open)+5, len(l.rows), ansi.StringWidth(cancel), 1, "command-cancel", 0)
		put("  " + open + "   " + cancel)
	}

	inputRows := homePromptRows(m.commands.query, width, true, mode, submit)
	if prompt.height < homePromptHeight {
		inputRows = []string{inputRows[1], inputRows[homePromptHeight-2], inputRows[homePromptHeight-1]}
	}

	l.hit(0, len(l.rows), width, len(inputRows), "command-input", 0)
	l.rows = append(l.rows, inputRows...)

	return l
}

func (m model) commandArea() hitArea {
	prompt := m.commandPromptArea()
	height := len(m.commandLayout().rows)

	return hitArea{x: prompt.x, y: prompt.y + prompt.height - height,
		width: prompt.width, height: height}
}

func (m model) commandView(view tea.View) tea.View {
	width, height := m.dimensions()
	if !m.commands.open || width < 48 || height < 16 {
		return view
	}

	lines := strings.Split(view.Content, "\n")
	area := m.commandArea()
	drawOverlay(width, lines, area, m.commandLayout().rows)

	if m.commands.agent {
		view.SetContent(strings.Join(lines, "\n"))
		return view
	}

	hint := "↑↓ choose · Enter select · Esc close"
	if m.commands.directory {
		hint = "↑↓ choose · Tab browse · Enter open · Esc cancel"
	}

	hint = compactHint(hint, area.width)
	drawOverlay(width, lines, hitArea{x: area.x, y: area.y + area.height, width: area.width},
		[]string{muted(hint) + strings.Repeat(" ", area.width-ansi.StringWidth(hint))})
	view.SetContent(strings.Join(lines, "\n"))

	return view
}

func (m model) updateCommandMouse(msg tea.MouseClickMsg) (tea.Model, tea.Cmd) {
	if msg.Button != tea.MouseLeft {
		return m, nil
	}

	area := m.commandArea()
	x, y := msg.X-area.x, msg.Y-area.y

	if x < 0 || x >= area.width || y < 0 || y >= area.height {
		m.commands.open = false
		return m, nil
	}

	for _, hit := range m.commandLayout().hits {
		if x < hit.x || x >= hit.x+hit.width || y < hit.y || y >= hit.y+hit.height {
			continue
		}

		switch hit.action {
		case "command":
			return m.chooseCommand(hit.index)
		case "directory":
			return m.browseDirectory(hit.index)
		case "directory-open":
			return m.openDirectory()
		case "command-cancel":
			m.commands.open = false
		case "command-input":
			m.commands.query.cursor = utf8.RuneCountInString(m.commands.query.value)
		}
	}

	return m, nil
}
