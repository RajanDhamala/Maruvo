package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

func agentInputFixture(t *testing.T) model {
	t.Helper()
	m := streamingFixture(t)
	m.localAgent.busy = false
	m.directory = t.TempDir()

	return m
}

func TestAgentFileMentionsCompleteWithoutSendingAndSupportSpaces(t *testing.T) {
	m := agentInputFixture(t)
	if err := os.Mkdir(filepath.Join(m.directory, "folder space"), 0700); err != nil {
		t.Fatal(err)
	}

	for name, content := range map[string]string{"README.md": "Read me", "folder space/plan.txt": "Private file content"} {
		if err := os.WriteFile(filepath.Join(m.directory, name), []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}

	next, cmd := m.Update(tea.PasteMsg{Content: "Check @read"})
	if cmd == nil {
		t.Fatal("paste did not start project file suggestions")
	}

	next, _ = next.Update(cmd())

	m = next.(model)
	if len(m.localAgent.files.items) != 1 || m.localAgent.files.items[0].label != "README.md" {
		t.Fatal("project file search did not find README")
	}

	next, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})

	m = next.(model)
	if m.localAgent.input.value != "Check @README.md " || m.localAgent.busy || m.localAgent.files.open {
		t.Fatal("Enter should insert a file reference without sending the prompt")
	}

	m.localAgent.input = textField{limit: 12000, byteLimit: 16000, multiline: true}
	next, cmd = m.Update(tea.PasteMsg{Content: "Read @fol"})
	next, _ = next.Update(cmd())

	next, cmd = next.Update(tea.KeyPressMsg{Code: tea.KeyTab})
	if cmd == nil {
		t.Fatal("Tab did not browse the selected directory")
	}

	next, _ = next.Update(cmd())
	next, _ = next.Update(tea.KeyPressMsg{Code: tea.KeyEnter})

	m = next.(model)
	if m.localAgent.input.value != `Read @"folder space/plan.txt" ` || m.localAgent.files.open ||
		strings.Contains(m.View().Content, "Private file content") {
		t.Fatal("quoted file reference failed or eagerly exposed file content")
	}

	next, cmd = m.Update(tea.PasteMsg{Content: "and @read"})
	if cmd == nil || !next.(model).localAgent.files.open {
		t.Fatal("an earlier quoted reference prevented another file search")
	}
}

func TestAgentFileSuggestionsStayInProjectAndAvoidCredentials(t *testing.T) {
	root, outside := t.TempDir(), t.TempDir()
	for _, name := range []string{"README.md", ".env", "secret.pem", "private.key", "session.json"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte("content"), 0600); err != nil {
			t.Fatal(err)
		}
	}

	if err := os.Symlink(outside, filepath.Join(root, "escape")); err != nil {
		t.Fatal(err)
	}

	for _, query := range []string{"../", "src/../../", outside + "/", "~/", "escape/", ".git/", ".ssh/"} {
		if files, err := agentFileSuggestions(root, query); err == nil || len(files) != 0 {
			t.Fatalf("unsafe file query accepted: %q", query)
		}
	}

	for _, query := range []string{"", ".env", "secret", "private", "session"} {
		files, err := agentFileSuggestions(root, query)
		if err != nil {
			t.Fatal(err)
		}

		for _, file := range files {
			if file.label != "README.md" {
				t.Fatalf("credential or symlink suggested: %s", file.label)
			}
		}
	}
}

func TestAgentSlashCommandsAndDismissalPreserveChat(t *testing.T) {
	m := agentInputFixture(t)
	m.width, m.height = 120, 36
	oldID := m.localAgent.chat.ID
	next, _ := m.Update(tea.PasteMsg{Content: "/"})

	m = next.(model)
	if !m.commands.open || !m.commands.agent || len(m.commandIndices()) == 0 {
		t.Fatal("agent chat did not expose the shared slash commands")
	}

	view := ansi.Strip(m.View().Content)
	for _, name := range []string{"/open", "/model", "/connect", "/agent", "/sessions", "/remote", "/new"} {
		if !strings.Contains(view, name) {
			t.Fatalf("agent command missing from menu: %s", name)
		}
	}
	if strings.Contains(view, "/submit") {
		t.Fatal("task commands must remain in the workspace")
	}

	next, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyBackspace})

	m = next.(model)
	if m.commands.open || !m.localAgent.open || m.localAgent.chat.ID != oldID ||
		m.localAgent.input.value != "" {
		t.Fatal("deleting the slash changed the chat or left the palette open")
	}

	next, _ = m.Update(tea.PasteMsg{Content: "/ordinary"})
	m = next.(model)
	m.commands.query.cursor = 0
	next, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyDelete})

	m = next.(model)
	if m.commands.open || m.localAgent.input.value != "ordinary" || m.homeInput.value != "" {
		t.Fatal("command dismissal did not restore ordinary typing in the agent input")
	}

	m.localAgent.input = textField{limit: 12000, byteLimit: 16000, multiline: true}
	next, _ = m.Update(tea.PasteMsg{Content: "/new"})
	next, _ = next.Update(tea.KeyPressMsg{Code: tea.KeyEnter})

	m = next.(model)
	if m.commands.open || m.localAgent.chat.ID != "" || len(m.localAgent.lines) != 0 || !m.localAgent.open {
		t.Fatal("agent /new must start a new chat without sending a model request")
	}
}

func TestBusyAgentRejectsTypingPasteAndCommandActions(t *testing.T) {
	m := streamingFixture(t)
	for _, message := range []tea.Msg{
		tea.KeyPressMsg{Code: 'a', Text: "a"}, tea.KeyPressMsg{Code: '/', Text: "/"},
		tea.KeyPressMsg{Code: tea.KeyEnter}, tea.KeyPressMsg{Code: tea.KeyTab},
		tea.PasteMsg{Content: "@README.md"}, tea.PasteMsg{Content: "/model"},
	} {
		next, _ := m.Update(message)
		m = next.(model)
	}

	if m.localAgent.input.value != "" || m.localAgent.files.open || m.commands.open || !m.localAgent.busy {
		t.Fatal("busy agent accepted input or opened suggestions")
	}

	if !strings.Contains(m.View().Content, "Input locked") || !strings.Contains(m.View().Content, "Working") {
		t.Fatal("busy composer does not explain its locked state")
	}
}

func TestAgentMenusFitAndMouseInsertsAFile(t *testing.T) {
	for _, size := range [][2]int{{48, 16}, {80, 24}, {120, 36}} {
		m := agentInputFixture(t)
		m.width, m.height = size[0], size[1]
		m.localAgent.input.insert("Check @read")

		m.localAgent.files = agentFilePicker{open: true, items: []localFile{{label: "README.md"}}}
		for _, line := range strings.Split(m.View().Content, "\n") {
			if ansi.StringWidth(line) > size[0] {
				t.Fatal("agent file suggestions overflowed the terminal")
			}
		}

		found := false

		for _, hit := range m.localAgentLayout().hits {
			if hit.action == "agent-file" {
				found = true

				next, _ := m.Update(tea.MouseClickMsg{Button: tea.MouseLeft, X: hit.x, Y: hit.y})
				if next.(model).localAgent.input.value != "Check @README.md " {
					t.Fatal("mouse did not insert the selected file")
				}
			}
		}

		if !found {
			t.Fatal("file suggestion has no accessible click target")
		}

		m.localAgent.input = textField{limit: 12000, byteLimit: 16000, multiline: true}
		next, _ := m.Update(tea.PasteMsg{Content: "/"})
		m = next.(model)

		area := m.commandArea()
		if area.y < 5 || area.y+area.height >= size[1] ||
			len(strings.Split(m.View().Content, "\n")) != size[1] {
			t.Fatalf("agent command palette obscures the header or exceeds the terminal at %v", size)
		}

		if strings.Count(ansi.Strip(m.View().Content), "Enter select · Esc close") != 1 {
			t.Fatal("agent command palette repeated its footer")
		}

		for _, line := range strings.Split(m.View().Content, "\n") {
			if ansi.StringWidth(line) > size[0] {
				t.Fatal("agent command palette overflowed the terminal")
			}
		}

		for range slashCommands {
			next, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyDown})
			m = next.(model)
		}

		if !strings.Contains(ansi.Strip(m.View().Content), "/new") {
			t.Fatal("small command palette did not scroll to the last command")
		}

		next, _ = m.chooseCommand(0)

		m = next.(model)
		if m.commandArea().y < 5 {
			t.Fatal("agent folder menu obscures the project header")
		}
	}
}
