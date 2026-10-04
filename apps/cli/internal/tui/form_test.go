package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/rajandhamala/Maruvo/cli/internal/api"
)

func TestDescriptionFileLimits(t *testing.T) {
	for name, test := range map[string]struct {
		content string
		valid   bool
	}{
		"multiline":           {"First line\n\tKeep indentation: café\n", true},
		"character boundary":  {strings.Repeat("a", api.MaxDescriptionCharacters), true},
		"too many characters": {strings.Repeat("a", api.MaxDescriptionCharacters+1), false},
		"byte boundary":       {strings.Repeat("😀", api.MaxDescriptionBytes/4), true},
		"too many bytes":      {strings.Repeat("😀", api.MaxDescriptionBytes/4+1), false},
		"binary":              {"text\x00binary", false},
		"invalid UTF-8":       {string([]byte{255, 254}), false},
		"terminal control":    {"text\x1b[31m", false},
		"empty":               {" \n\t", false},
	} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "task.txt")
			if err := os.WriteFile(path, []byte(test.content), 0600); err != nil {
				t.Fatal(err)
			}

			text, err := readDescription(path)
			if (err == nil) != test.valid {
				t.Fatalf("valid=%v, error=%v", test.valid, err)
			}

			if test.valid && text != test.content {
				t.Fatal("file text must retain newlines and indentation")
			}
		})
	}
}

func TestCreationKeepsTextWithoutFileDeclarations(t *testing.T) {
	m := model{screen: newPostScreen, token: "test", width: 80, height: 24, form: newPostForm()}
	m.form.timings[1].insert(time.Now().Add(7 * 24 * time.Hour).Format("2006-01-02 15:04"))
	m.form.descriptionSource = descriptionFromText
	m.form.fields[0].insert("Fix parser")
	m.form.focus = 3
	next, _ := m.Update(tea.PasteMsg{Content: "Read the source.\n\tKeep UTF-8: café"})
	m = next.(model)
	next, cmd := m.updateForm(tea.KeyPressMsg{Code: tea.KeyEnter})

	m = next.(model)
	if cmd != nil || m.loading || !strings.HasSuffix(m.form.fields[3].value, "\n") {
		t.Fatal("Enter must insert a newline without publishing")
	}

	m.form.fields[4].insert("The parser handles UTF-8.\nAll tests pass.")

	payload, err := m.form.payload()
	if err != nil {
		t.Fatal(err)
	}

	if payload.Description != m.form.fields[3].value ||
		payload.AcceptanceCriteria != m.form.fields[4].value ||
		len(payload.InputFiles) != 0 ||
		len(payload.ExpectedOutputs) != 0 {
		t.Fatal("creation must keep the description and plain outcome without requiring filenames")
	}

	before := m.form.fields[3].value
	m = m.openDescriptionImport()
	next, _ = m.descriptionLoaded(descriptionLoaded{err: os.ErrNotExist})

	m = next.(model)
	if m.form.fields[3].value != before || !m.form.importing || m.err == nil {
		t.Fatal("a failed import must preserve the draft")
	}
}

func TestFormFocusAndPublishStayVisible(t *testing.T) {
	for _, size := range [][2]int{{48, 16}, {64, 20}, {80, 24}, {120, 36}} {
		for focus := 0; focus < 8; focus++ {
			m := model{
				screen: newPostScreen,
				token:  "test",
				width:  size[0],
				height: size[1],
				form:   newPostForm(),
			}
			m.form.focus = focus
			m.form.descriptionSource = descriptionFromText
			m.form.fields[3].insert(strings.Repeat("Line with UTF-8: café\n", 12))

			layout := m.formLayout()
			if len(layout.rows) > m.bodyHeight() ||
				!strings.Contains(ansi.Strip(layout.rows[len(layout.rows)-1]), "Publish task →") {
				t.Fatalf("%dx%d focus %d: publishing must remain visible", size[0], size[1], focus)
			}

			for _, row := range layout.rows {
				if ansi.StringWidth(row) > m.contentWidth() {
					t.Fatalf("%dx%d: form row overflow", size[0], size[1])
				}
			}

			if focus == 3 || focus == 4 {
				if !strings.Contains(strings.Join(layout.rows, "\n"), "\x1b[7m") {
					t.Fatal("the text cursor must be visible")
				}
			}

			if focus == 0 && strings.Contains(layout.rows[len(layout.rows)-1], ";44m") {
				t.Fatal("Publish must not look focused while editing the title")
			}
		}
	}
}

func TestMultilineCursorAcrossWrappedUnicode(t *testing.T) {
	f := textField{limit: 12000, multiline: true}
	f.insert("abc café\n\tsecond line\nlast")
	f.multilineKey(tea.KeyPressMsg{Code: tea.KeyUp}, 12)

	if f.cursor < 0 || f.cursor > utf8.RuneCountInString(f.value) {
		t.Fatal("cursor must stay in the text")
	}

	rows, _ := f.textRows(12, 2, true, "")
	if !strings.Contains(strings.Join(rows, "\n"), "\x1b[7m") {
		t.Fatal("wrapped cursor must remain visible")
	}
}

func TestDescriptionRequiresSourceSelection(t *testing.T) {
	m := dashboardFixture()
	m.screen, m.form = newPostScreen, newPostForm()
	m.form.timings[1].insert(time.Now().Add(7 * 24 * time.Hour).Format("2006-01-02 15:04"))
	m.form.focus = 3
	m.form.fields[0].insert("File first task")
	next, _ := m.Update(tea.PasteMsg{Content: "Should not enter hidden text"})
	next, _ = next.Update(tea.KeyPressMsg{Code: 'x', Text: "x"})

	m = next.(model)
	if m.form.descriptionSource != descriptionFromFile || m.form.fields[3].value != "" {
		t.Fatal("description should default to file selection and ignore typing or paste")
	}

	if _, err := m.form.payload(); err == nil {
		t.Fatal("the default description needs a file or an explicit text selection")
	}

	opened, command := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if command == nil || !opened.(model).form.importing {
		t.Fatal("Enter must open file selection instead of a text editor")
	}

	next, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyRight})
	next, _ = next.Update(tea.PasteMsg{Content: "An explicitly typed description."})

	m = next.(model)
	if m.form.descriptionSource != descriptionFromText {
		t.Fatal("the alternative source must explicitly enable text entry")
	}

	payload, err := m.form.payload()
	if err != nil || payload.Description != "An explicitly typed description." {
		t.Fatal("selected text mode must publish the user's typed description")
	}

	for _, hit := range m.formLayout().hits {
		if hit.action != "description-source" || hit.index != descriptionFromFile || hit.height == 0 {
			continue
		}

		next, _ = m.Update(tea.MouseClickMsg{X: m.contentX() + hit.x,
			Y: m.bodyStart() + hit.y, Button: tea.MouseLeft})

		m = next.(model)
		if _, err = m.form.payload(); err == nil {
			t.Fatal("File mode must require a selected file rather than sending hidden typed text")
		}

		m = m.editDescription()
		if m.form.fields[3].value != payload.Description {
			t.Fatal("returning to Write text should retain the earlier draft")
		}

		return
	}

	t.Fatal("File source must be clickable")
}

func TestDescriptionSearchLoadsSelectedFile(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "README.md")

	content := "Fix the parser.\n\tPreserve café and indentation.\n"
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}

	m := model{screen: newPostScreen, token: "test", width: 80, height: 24, form: newPostForm()}
	m.form.timings[1].insert(time.Now().Add(7 * 24 * time.Hour).Format("2006-01-02 15:04"))
	m.form.fields[0].insert("Parser task")
	m.form.fields[3].insert("Existing draft")
	m = m.openDescriptionImport()
	m.form.fileSearch.root = root

	next, command := m.Update(tea.PasteMsg{Content: "/readme"})
	m = next.(model)

	if command == nil {
		t.Fatal("pasting a search must request suggestions")
	}

	next, _ = m.Update(command())

	m = next.(model)
	if len(m.form.fileSearch.suggestions) != 1 || m.form.fileSearch.suggestions[0].path != path {
		t.Fatalf("/readme must find the project README: %v", m.form.fileSearch.suggestions)
	}

	next, command = m.updateForm(tea.KeyPressMsg{Code: tea.KeyEnter})

	m = next.(model)
	if command == nil || !m.loading || m.form.fields[3].value != "Existing draft" {
		t.Fatal("selection must read the file before replacing the draft")
	}

	next, _ = m.Update(command())

	m = next.(model)
	if m.err != nil || m.form.importing || m.loading || m.form.fields[3].value != content {
		t.Fatalf("selected file did not become editable description text: %v", m.err)
	}

	if m.form.descriptionFile != "README.md" || strings.Contains(m.View().Content, "Fix the parser.") ||
		!strings.Contains(m.View().Content, "README.md") {
		t.Fatal("the imported description must show its filename while retaining the text for publication")
	}

	payload, err := m.form.payload()
	if err != nil || payload.Description != content || len(payload.InputFiles) != 0 {
		t.Fatalf("search import changed the creation contract: %v", err)
	}
}

func TestImportedDescriptionEditRemoveAndReplace(t *testing.T) {
	base := dashboardFixture()
	base.screen, base.form = newPostScreen, newPostForm()
	base.form.fields[0].insert("Parser task")
	base = base.openDescriptionImport()
	next, _ := base.Update(descriptionLoaded{text: "Keep UTF-8: café\n\tIndented line.", name: "brief.md"})
	base = next.(model)
	text := base.form.fields[3].value

	for _, action := range []string{"edit", "remove", "replace", "paste"} {
		t.Run(action, func(t *testing.T) {
			m := base
			if action == "paste" {
				m = m.editDescription()
				next, _ := m.Update(tea.PasteMsg{Content: "\nOne more line."})

				m = next.(model)
				if m.form.descriptionFile != "" || m.form.fields[3].value != text+"\nOne more line." {
					t.Fatal(
						"pasting into a file description should reveal editable text and retain its contents",
					)
				}

				return
			}

			target := map[string]string{"edit": "description-source", "remove": "description-remove", "replace": "description-import"}[action]
			found := false

			for _, hit := range m.formLayout().hits {
				if hit.action != target || hit.height == 0 ||
					(action == "edit" && hit.index != descriptionFromText) {
					continue
				}

				found = true
				next, _ := m.Update(tea.MouseClickMsg{X: m.contentX() + hit.x,
					Y: m.bodyStart() + hit.y, Button: tea.MouseLeft})
				m = next.(model)

				break
			}

			if !found {
				t.Fatal("file action must be clickable")
			}

			switch action {
			case "edit":
				if m.form.descriptionFile != "" || m.form.fields[3].value != text ||
					m.form.descriptionSource != descriptionFromText ||
					!strings.Contains(m.View().Content, "Indented line.") {
					t.Fatal("Write text should reveal the imported description without changing it")
				}
			case "remove":
				if m.form.descriptionFile != "" || m.form.fields[3].value != "" ||
					m.form.descriptionSource != descriptionFromFile {
					t.Fatal("Remove must clear the filename and text while retaining file selection")
				}

				if _, err := m.form.payload(); err == nil {
					t.Fatal("the removed file's text must not be silently published")
				}
			case "replace":
				next, _ := m.Update(descriptionLoaded{err: os.ErrNotExist})

				m = next.(model)
				if !m.form.importing || m.form.descriptionFile != "brief.md" ||
					m.form.fields[3].value != text {
					t.Fatal("a failed replacement must retain the loaded file and its text")
				}

				next, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEsc})

				m = next.(model)
				if m.form.importing || !strings.Contains(m.View().Content, "brief.md") {
					t.Fatal("canceling file search must restore the previous filename")
				}
			}
		})
	}
}

func TestCreationThemeAndNavigation(t *testing.T) {
	for _, size := range [][2]int{{48, 16}, {64, 20}, {80, 24}, {120, 36}, {168, 44}} {
		for _, state := range []string{"idle", "empty file", "file", "text", "search", "deadline", "profile", "publishing", "error"} {
			m := dashboardFixture()
			m.screen, m.form = newPostScreen, newPostForm()
			m.width, m.height = size[0], size[1]

			switch state {
			case "empty file":
				m.form.focus = 3
			case "file":
				m.form.focus, m.form.descriptionFile = 3, "brief.md"
				m.form.fields[3].insert("Imported description is retained internally.")
			case "text":
				m = m.editDescription()
				m.form.fields[3].insert("Editable description text.")
			case "search":
				m = m.openDescriptionImport()
				m.form.path.insert("/readme")
				m.form.fileSearch.suggestions = []localFile{{label: "README.md", path: "README.md"}}
			case "deadline":
				m = m.openDeadline()
			case "profile":
				m.profileOpen = true
			case "publishing":
				m.loading = true
			case "error":
				m.err = os.ErrNotExist
			}

			view := m.View()

			rows := strings.Split(view.Content, "\n")
			if len(rows) != size[1] || view.BackgroundColor == nil || view.ForegroundColor == nil ||
				strings.Contains(view.Content, "1 Feed") || strings.Contains(view.Content, "2 My Tasks") {
				t.Fatalf("%v %s: creation must use the home theme and navigation", size, state)
			}

			for _, row := range rows {
				if ansi.StringWidth(row) > size[0] {
					t.Fatalf("%v %s: view overflow", size, state)
				}
			}

			if state == "file" && (!strings.Contains(view.Content, "brief.md") ||
				strings.Contains(view.Content, "Imported description")) {
				t.Fatalf("%v: the loaded file should display its filename without dumping its text", size)
			}

			if (state == "file" || state == "empty file") &&
				(!strings.Contains(view.Content, "Write text") || strings.Contains(view.Content, "\x1b[7m")) {
				t.Fatalf("%v: file selection must show the alternative without a text cursor", size)
			}
		}

		m := dashboardFixture()
		m.width, m.height = size[0], size[1]
		m.screen, m.form = newPostScreen, newPostForm()
		m.homeInput = textField{value: "Keep the home draft", cursor: 19, limit: 2000}
		next, _ := m.Update(tea.MouseClickMsg{X: m.contentX() + 1, Y: 3, Button: tea.MouseLeft})

		m = next.(model)
		if !m.onDashboard() || m.homeInput.value != "Keep the home draft" {
			t.Fatal("Back must return to the dashboard and retain the prompt draft")
		}
	}
}

func TestCreationPaddedFieldMouse(t *testing.T) {
	m := dashboardFixture()
	m.height = 44
	m.screen, m.form = newPostScreen, newPostForm()
	m.form.descriptionSource = descriptionFromText
	m.form.focus = 3
	m.form.fields[3].insert("abc café\nsecond line")

	for _, hit := range m.formLayout().hits {
		if hit.action != "field" || hit.index != 3 || hit.height == 0 {
			continue
		}

		next, _ := m.Update(tea.MouseClickMsg{X: m.contentX() + hit.x + 6,
			Y: m.bodyStart() + hit.y + 2, Button: tea.MouseLeft})

		m = next.(model)
		if m.form.fields[3].cursor != 4 {
			t.Fatal("clicking the first padded text row must place the cursor at the visible column")
		}

		next, _ = m.Update(tea.MouseClickMsg{X: m.contentX() + hit.x + 6,
			Y: m.bodyStart() + hit.y + 1, Button: tea.MouseLeft})

		m = next.(model)
		if m.form.fields[3].cursor != 4 {
			t.Fatal("clicking input padding must focus the field without moving the text cursor")
		}

		return
	}

	t.Fatal("focused description needs a mouse target")
}

func TestDescriptionSearchPathsAndStaleResults(t *testing.T) {
	root := t.TempDir()

	directory := filepath.Join(root, "docs")
	if err := os.Mkdir(directory, 0700); err != nil {
		t.Fatal(err)
	}

	path := filepath.Join(directory, "task brief.md")
	if err := os.WriteFile(path, []byte("A task brief."), 0600); err != nil {
		t.Fatal(err)
	}

	for _, query := range []string{"task brief", "docs/task"} {
		files, err := descriptionFileSuggestions(root, query)
		if err != nil || len(files) != 1 || files[0].path != path {
			t.Fatalf("search %q: files=%v err=%v", query, files, err)
		}
	}

	for _, query := range []string{path, "~/task brief.md", "../task brief.md"} {
		files, err := descriptionFileSuggestions(root, query)
		if err == nil || len(files) != 0 {
			t.Fatalf("manual paths outside the project must not be accepted: %q", query)
		}
	}

	m := model{screen: newPostScreen, token: "test", width: 80, height: 24, form: newPostForm()}
	m = m.openDescriptionImport()
	m.form.fileSearch.root = root
	oldCommand := m.searchDescriptionFiles()
	m.form.path.insert("docs/")
	newCommand := m.searchDescriptionFiles()
	next, _ := m.Update(oldCommand())

	m = next.(model)
	if !m.form.fileSearch.searching || len(m.form.fileSearch.suggestions) != 0 {
		t.Fatal("an old search must not replace the current suggestions")
	}

	next, _ = m.Update(newCommand())

	m = next.(model)
	if m.form.fileSearch.searching || len(m.form.fileSearch.suggestions) != 1 {
		t.Fatal("the latest path search must show its results")
	}

	next, _ = m.updateForm(tea.KeyPressMsg{Code: tea.KeyEsc})
	m = next.(model)
	m = m.openDescriptionImport()
	m.form.fileSearch.root = root
	_ = m.searchDescriptionFiles()
	next, _ = m.Update(newCommand())

	m = next.(model)
	if !m.form.fileSearch.searching || len(m.form.fileSearch.suggestions) != 0 {
		t.Fatal("reopening search must reject results from its previous session")
	}
}

func TestDescriptionSearchFitsAndSupportsMouse(t *testing.T) {
	root := t.TempDir()

	path := filepath.Join(root, "README.md")
	if err := os.WriteFile(path, []byte("Task description."), 0600); err != nil {
		t.Fatal(err)
	}

	for _, size := range [][2]int{{48, 16}, {64, 20}, {80, 24}, {120, 36}} {
		m := model{screen: newPostScreen, token: "test", width: size[0], height: size[1], form: newPostForm()}
		m = m.openDescriptionImport()
		m.form.path.insert("/readme")
		m.form.fileSearch.suggestions = []localFile{{path: path, label: "README.md"}}

		layout := m.formLayout()
		if len(layout.rows) > m.bodyHeight() {
			t.Fatalf("%dx%d: search rows exceed the screen", size[0], size[1])
		}

		var searchVisible, fileVisible bool

		for _, row := range layout.rows {
			if ansi.StringWidth(row) > m.contentWidth() {
				t.Fatalf("%dx%d: search row overflow", size[0], size[1])
			}

			searchVisible = searchVisible || strings.Contains(ansi.Strip(row), "/readme")
			fileVisible = fileVisible || strings.Contains(ansi.Strip(row), "README.md")
		}

		if !searchVisible || !fileVisible || !strings.Contains(layout.footer, "Enter load") {
			t.Fatalf("%dx%d: search input, selection, and help must be visible", size[0], size[1])
		}

		for _, hit := range layout.hits {
			if hit.action != "description-file" || hit.height == 0 {
				continue
			}

			next, command := m.updateMouse(tea.MouseClickMsg{
				Button: tea.MouseLeft, X: m.contentX() + hit.x, Y: m.bodyStart() + hit.y,
			})

			m = next.(model)
			if command == nil || !m.loading || m.form.path.value != "README.md" {
				t.Fatal("clicking a search result must load the selected file")
			}
		}
	}
}

func TestDescriptionSearchBrowsesDirectoriesAndPreservesDraft(t *testing.T) {
	root := t.TempDir()

	directory := filepath.Join(root, "docs")
	if err := os.Mkdir(directory, 0700); err != nil {
		t.Fatal(err)
	}

	path := filepath.Join(directory, "binary.txt")
	if err := os.WriteFile(path, []byte("text\x00binary"), 0600); err != nil {
		t.Fatal(err)
	}

	m := model{screen: newPostScreen, token: "test", width: 80, height: 24, form: newPostForm()}
	m.form.fields[3].insert("Keep this draft.")
	m = m.openDescriptionImport()
	m.form.fileSearch.root = root
	command := m.searchDescriptionFiles()
	next, _ := m.Update(command())
	m = next.(model)

	next, command = m.updateForm(tea.KeyPressMsg{Code: tea.KeyEnter})

	m = next.(model)
	if command == nil || m.loading || m.form.path.value != "docs/" {
		t.Fatal("selecting a directory must browse it")
	}

	next, _ = m.Update(command())
	m = next.(model)
	next, command = m.updateForm(tea.KeyPressMsg{Code: tea.KeyTab})

	m = next.(model)
	if command == nil || m.loading || m.form.path.value != "docs/binary.txt" {
		t.Fatal("Tab must complete the selected path without loading it")
	}

	next, _ = m.Update(command())
	m = next.(model)
	next, command = m.updateForm(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = next.(model)
	next, _ = m.Update(command())

	m = next.(model)
	if m.err == nil || !m.form.importing || m.loading || m.form.fields[3].value != "Keep this draft." {
		t.Fatal("a rejected file must retain the draft and leave search editable")
	}
}
