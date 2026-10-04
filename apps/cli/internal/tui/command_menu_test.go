package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

func TestSlashCommandsPreserveDraftAndSearch(t *testing.T) {
	m := dashboardFixture()
	m.homeInput = textField{value: "Keep this draft", cursor: 15, limit: 2000}
	m.dashboard.search = textField{value: "tests", cursor: 5, limit: 200}

	next, cmd := m.Update(tea.KeyPressMsg{Code: '/', Text: "/"})

	m = next.(model)
	if cmd != nil || !m.commands.open || m.dashboard.focus != dashboardTasks ||
		m.homeInput.value != "Keep this draft" || m.dashboard.search.value != "tests" {
		t.Fatal("slash must open commands without changing the draft or task search")
	}

	next, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})

	m = next.(model)
	if m.commands.open || m.homeInput.value != "Keep this draft" {
		t.Fatal("cancel must preserve the prompt")
	}

	next, _ = m.Update(tea.KeyPressMsg{Code: 'f', Mod: tea.ModCtrl})
	if next.(model).dashboard.focus != dashboardSearch {
		t.Fatal("Ctrl+f must retain keyboard access to task search")
	}

	for _, signedIn := range []bool{false, true} {
		m := dashboardFixture()
		m.dashboard.focus = dashboardPrompt

		if !signedIn {
			m.token = ""
		}

		next, _ := m.Update(tea.KeyPressMsg{Code: '/', Text: "/"})
		if !next.(model).commands.open || next.(model).homeInput.value != "" {
			t.Fatal("empty home prompts should open commands before or after sign-in")
		}

		m.homeInput = textField{value: "ordinary", cursor: 8, limit: 2000}

		next, _ = m.Update(tea.KeyPressMsg{Code: '/', Text: "/"})
		if next.(model).commands.open || next.(model).homeInput.value != "ordinary/" {
			t.Fatal("slashes within an existing prompt must remain ordinary text")
		}
	}
}

func TestDirectoryCommandOpensSelectedFolder(t *testing.T) {
	root := t.TempDir()

	folder := filepath.Join(root, "café project")
	if err := os.Mkdir(folder, 0700); err != nil {
		t.Fatal(err)
	}

	brief := filepath.Join(folder, "brief.md")
	if err := os.WriteFile(brief, []byte("Task instructions."), 0600); err != nil {
		t.Fatal(err)
	}

	m := dashboardFixture()
	m.directory = root
	m.homeInput = textField{value: "Keep the draft", cursor: 14, limit: 2000}
	m = m.openCommands()
	next, _ := m.Update(tea.PasteMsg{Content: "open"})

	next, cmd := next.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("selecting /open should read directory suggestions")
	}

	next, _ = next.Update(cmd())
	m = next.(model)

	if !m.commands.directory || len(m.commands.folders) != 2 || m.commands.folders[1].path != folder {
		t.Fatal("folder picker should list local directories")
	}

	next, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	next, _ = next.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	next, cmd = next.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	next, _ = next.Update(cmd())
	m = next.(model)

	if m.commands.open || m.directory != folder || m.homePath != displayHomePath(folder) ||
		m.homeInput.value != "Keep the draft" || m.composer.root != folder {
		t.Fatal("opening a directory should update local roots and the footer while retaining the prompt")
	}

	m.screen, m.form = newPostScreen, newPostForm()
	m = m.openDescriptionImport()

	files, err := descriptionFileSuggestions(m.form.fileSearch.root, "/brief")
	if err != nil || len(files) != 1 || files[0].path != filepath.Join(folder, "brief.md") {
		t.Fatal("description import must search within the opened directory")
	}

	if _, err := descriptionFileSuggestions(m.form.fileSearch.root, "../brief.md"); err == nil {
		t.Fatal("opening a directory must preserve project-bound description search")
	}
}

func TestDirectoryCommandInvalidPathsAndCanceledResults(t *testing.T) {
	root := t.TempDir()

	file := filepath.Join(root, "file.txt")
	if err := os.WriteFile(file, []byte("text"), 0600); err != nil {
		t.Fatal(err)
	}

	for _, path := range []string{file, filepath.Join(root, "missing")} {
		m := dashboardFixture()
		m.directory = root
		m = m.openCommands()
		next, _ := m.chooseCommand(0)
		m = next.(model)
		m.commands.query = textField{value: path, cursor: utf8.RuneCountInString(path), limit: 4096}
		next, cmd := m.openDirectory()
		next, _ = next.Update(cmd())

		m = next.(model)
		if !m.commands.open || m.commands.err == nil || m.directory != root {
			t.Fatal("files and invalid folders must keep the picker open without changing the root")
		}
	}

	m := dashboardFixture()
	m.directory = root
	m = m.openCommands()
	next, _ := m.chooseCommand(0)
	m = next.(model)

	folder := filepath.Join(root, "canceled")
	if err := os.Mkdir(folder, 0700); err != nil {
		t.Fatal(err)
	}

	m.commands.query = textField{value: folder, cursor: utf8.RuneCountInString(folder), limit: 4096}
	next, cmd := m.openDirectory()
	opened := cmd()
	next, _ = next.Update(tea.KeyPressMsg{Code: tea.KeyEscape})

	next, _ = next.Update(opened)
	if next.(model).directory != root || next.(model).commands.open {
		t.Fatal("a canceled picker must ignore pending open results")
	}

	next, _ = m.Update(directoriesFound{sequence: m.commands.sequence - 1,
		folders: []localFile{{path: "stale", directory: true}}})
	if len(next.(model).commands.folders) != 0 {
		t.Fatal("old folder searches must not replace the current suggestions")
	}
}

func TestDirectoryPathExpansionAndBrowsing(t *testing.T) {
	root := t.TempDir()

	folder := filepath.Join(root, "nested")
	if err := os.Mkdir(folder, 0700); err != nil {
		t.Fatal(err)
	}

	if err := os.Symlink(folder, filepath.Join(root, "linked")); err != nil {
		t.Fatal(err)
	}

	t.Setenv("HOME", root)

	for _, query := range []string{"nested", folder, "~/nested"} {
		path, err := directoryPath(root, query)
		if err != nil || path != folder {
			t.Fatalf("path %q: %s, %v", query, path, err)
		}
	}

	m := dashboardFixture()
	m.directory = root
	m = m.openCommands()
	next, cmd := m.chooseCommand(0)
	next, _ = next.Update(cmd())

	m = next.(model)
	if len(m.commands.folders) != 3 || m.commands.folders[0].label != ".." {
		t.Fatal("picker must allow normal and symlinked directories")
	}

	next, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	next, _ = next.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	next, cmd = next.Update(tea.KeyPressMsg{Code: tea.KeyTab})
	next, _ = next.Update(cmd())

	m = next.(model)
	if m.commands.query.value != filepath.Join(root, "linked")+string(filepath.Separator) {
		t.Fatal("Tab should browse the selected directory without applying it")
	}

	next, cmd = m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})

	next, _ = next.Update(cmd())
	if next.(model).directory != folder {
		t.Fatal("opening a symlink should resolve the selected directory")
	}
}

func TestCommandMenuResponsiveLayoutAndMouse(t *testing.T) {
	for _, size := range [][2]int{{48, 16}, {64, 20}, {80, 24}, {120, 36}, {168, 44}, {240, 60}} {
		for _, signedIn := range []bool{false, true} {
			for _, directory := range []bool{false, true} {
				m := dashboardFixture()

				m.width, m.height = size[0], size[1]
				if !signedIn {
					m.token = ""
				}

				baseline := strings.Split(m.View().Content, "\n")
				m = m.openCommands()

				m.commands.directory = directory
				if directory {
					m.commands.selection = 7
					for i := 0; i < 8; i++ {
						m.commands.folders = append(m.commands.folders,
							localFile{label: "folder", path: "folder", directory: true})
					}
				}

				rows := strings.Split(m.View().Content, "\n")
				if len(rows) != size[1] {
					t.Fatalf("%v: menu changed the terminal row count", size)
				}

				for _, row := range rows {
					if ansi.StringWidth(row) > size[0] {
						t.Fatalf("%v: command menu overflow", size)
					}
				}

				area := m.commandArea()

				promptX, promptY, promptWidth := 2, size[1]-homePromptHeight-3, m.dashboardGeometry().mainWidth
				if !signedIn {
					promptWidth = min(104, size[0]-4)
					promptX, promptY = (size[0]-promptWidth)/2, size[1]-homePromptHeight-4
				}

				if area.x != promptX || area.width != promptWidth ||
					area.y+area.height-homePromptHeight != promptY {
					t.Fatalf("%v: menu must attach to the bottom prompt: %+v", size, area)
				}

				for y := area.y; y < area.y+area.height; y++ {
					if ansi.Strip(ansi.Cut(rows[y], area.x+area.width, size[0])) !=
						ansi.Strip(ansi.Cut(baseline[y], area.x+area.width, size[0])) {
						t.Fatalf("%v: menu must preserve the sidebar and right margin", size)
					}
				}

				for _, hit := range m.commandLayout().hits {
					if area.x+hit.x < 0 || area.x+hit.x+hit.width > size[0] ||
						area.y+hit.y < 0 || area.y+hit.y+hit.height > size[1] {
						t.Fatalf("%v: inaccessible command control %+v", size, hit)
					}

					if !directory && hit.action == "command" && hit.index == 0 {
						next, cmd := m.Update(tea.MouseClickMsg{Button: tea.MouseLeft,
							X: area.x + hit.x, Y: area.y + hit.y})
						if cmd == nil || !next.(model).commands.directory {
							t.Fatal("clicking Open directory must open the picker")
						}
					}
				}
			}
		}
	}
}

func TestCommandMenuUsesBottomInputAndRestoresHiddenPrompt(t *testing.T) {
	m := dashboardFixture()
	m.dashboard.promptHidden = true
	m.homeInput = textField{value: "Keep this draft", cursor: 15, limit: 2000}
	baseline := m.View().Content
	m = m.openCommands()
	m.commands.query = textField{value: "/pri", cursor: 4, limit: 100}
	rows := strings.Split(m.View().Content, "\n")
	prompt := m.commandPromptArea()
	input := ansi.Strip(rows[prompt.y+1])

	if !strings.Contains(input, "/pri") || strings.Contains(input, m.homeInput.value) ||
		!m.dashboard.promptHidden || m.homeInput.value != "Keep this draft" {
		t.Fatal("commands must use the bottom input while preserving the hidden task prompt")
	}

	next, _ := m.Update(tea.MouseClickMsg{Button: tea.MouseLeft, X: prompt.x + 3, Y: prompt.y + 1})
	if !next.(model).commands.open {
		t.Fatal("clicking the command input must retain the menu")
	}

	next, _ = next.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	if next.View().Content != baseline {
		t.Fatal("canceling must restore the original dashboard and hidden prompt")
	}
}
