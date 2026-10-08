package tui

import (
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/rajandhamala/Maruvo/cli/internal/providers"
)

func TestSessionPickerRestoresChatFolderDraftAndContextAfterRestart(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	m := dashboardFixture()
	scope := providers.ChatScope{Profile: m.profile, APIURL: m.client.URL(), Account: m.user.ID}

	chat, err := providers.NewConversation(t.TempDir(), "Go server timeout", "deepseek", "deepseek-flash")
	if err != nil {
		t.Fatal(err)
	}

	chat.Messages = []providers.ChatMessage{
		{Role: "user", Content: "Read the server"},
		{Role: "assistant", Content: "Use **timeouts**."},
	}
	chat.Lines = []string{"You: Read the server", "Tool: … Read file · main.go", "Agent: Use **timeouts**."}

	chat.Draft, chat.Pending = "Add a test", true
	if err = providers.SaveConversation(scope, chat); err != nil {
		t.Fatal(err)
	}

	m.token = ""
	m.localAgent = localAgentState{open: true, chatScope: scope}
	next, cmd := m.openLocalSessions()
	next, _ = next.Update(cmd())

	m = next.(model)

	m.localAgent.sessions.allFolders = true
	if len(m.localAgent.sessions.items) != 1 || !m.localAgent.sessions.open {
		t.Fatal("restart history unavailable")
	}

	for _, size := range [][2]int{{48, 16}, {80, 24}, {120, 36}} {
		m.width, m.height = size[0], size[1]

		view := ansi.Strip(m.View().Content)
		if !strings.Contains(view, "Go server timeout") ||
			!strings.Contains(view, "Resume a previous session") {
			t.Fatal("history picker hid the saved chat")
		}

		for _, line := range strings.Split(view, "\n") {
			if ansi.StringWidth(line) > size[0] {
				t.Fatal("history picker overflowed")
			}
		}
	}

	next, cmd = m.updateLocalSessions(tea.KeyPressMsg{Code: tea.KeyEnter})
	next, _ = next.Update(cmd())

	m = next.(model)
	if m.localAgent.sessions.open || m.directory != chat.Directory ||
		m.localAgent.input.value != chat.Draft ||
		len(m.localAgent.history) != 2 ||
		!strings.Contains(m.localAgent.lines[1], "Interrupted; result unknown") {
		t.Fatal("resuming lost the folder, draft, context or interrupted state")
	}

	next, cmd = m.newLocalConversation()
	if cmd != nil {
		next, _ = next.Update(cmd())
	}

	m = next.(model)
	if m.localAgent.chat.ID != "" || len(m.localAgent.lines) != 0 {
		t.Fatal("new chat retained old context")
	}

	list, err := providers.ListConversations(scope)
	if err != nil || len(list) != 1 {
		t.Fatalf("new chat deleted history: %+v %v", list, err)
	}
}

func TestSessionFiltersSortingAndArchive(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	m := dashboardFixture()
	m.token = ""
	m.directory = t.TempDir()
	scope := providers.ChatScope{Profile: "worker", Account: "7"}
	current, _ := providers.NewConversation(m.directory, "Current project", "deepseek", "deepseek-flash")
	other, _ := providers.NewConversation(t.TempDir(), "Another project", "deepseek", "deepseek-flash")
	current.CreatedAt = current.CreatedAt.Add(-2 * time.Hour)

	current.UpdatedAt = current.UpdatedAt.Add(time.Hour)
	for _, chat := range []providers.Conversation{current, other} {
		if err := providers.SaveConversation(scope, chat); err != nil {
			t.Fatal(err)
		}
	}

	m.localAgent = localAgentState{open: true, chatScope: scope}
	next, cmd := m.openLocalSessions()
	next, _ = next.Update(cmd())

	m = next.(model)
	if ids := m.sessionIndices(); len(ids) != 1 || m.localAgent.sessions.items[ids[0]].ID != current.ID {
		t.Fatal("default Cwd filter exposed another project")
	}

	next, _ = m.updateLocalSessions(tea.KeyPressMsg{Code: 'f', Mod: tea.ModCtrl})
	m = next.(model)

	ids := m.sessionIndices()
	if len(ids) != 2 || m.localAgent.sessions.items[ids[0]].ID != current.ID {
		t.Fatal("All filter or Updated ordering failed")
	}

	next, _ = m.updateLocalSessions(tea.KeyPressMsg{Code: 'o', Mod: tea.ModCtrl})
	m = next.(model)

	ids = m.sessionIndices()
	if m.localAgent.sessions.items[ids[0]].ID != other.ID {
		t.Fatal("Created sort did not change ordering")
	}

	m.localAgent.chat = other

	next, cmd = m.archiveLocalSession()
	if !next.(model).localAgent.chat.Archived {
		t.Fatal("archiving the current chat did not update its pending save")
	}

	next, _ = next.Update(cmd())

	m = next.(model)
	if len(m.sessionIndices()) != 1 {
		t.Fatal("archived chat stayed in Active")
	}

	next, _ = m.updateLocalSessions(tea.KeyPressMsg{Code: 's', Mod: tea.ModCtrl})

	m = next.(model)
	if ids = m.sessionIndices(); len(ids) != 1 || m.localAgent.sessions.items[ids[0]].ID != other.ID {
		t.Fatal("Archived filter lost the chat")
	}

	next, cmd = m.archiveLocalSession()
	result := cmd()

	m = next.(model)
	if m.localAgent.chat.Archived {
		t.Fatal("restoring the current chat left a stale archive flag")
	}

	if saved := m.saveLocalConversation()().(localConversationSaved); saved.err != nil {
		t.Fatal(saved.err)
	}

	next, _ = m.Update(result)

	m = next.(model)
	if len(m.sessionIndices()) != 0 {
		t.Fatal("restored chat remained archived")
	}

	loaded, err := providers.LoadConversation(scope, other.Directory, other.ID)
	if err != nil || loaded.Archived {
		t.Fatal("archive/restore was not saved")
	}
}

func TestSavedChatRedactsKnownCredentials(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	m := dashboardFixture()

	client, err := providers.NewClient("deepseek", "deepseek-flash", "provider-secret")
	if err != nil {
		t.Fatal(err)
	}

	chat, err := providers.NewConversation(t.TempDir(), "provider-secret", "deepseek", "deepseek-flash")
	if err != nil {
		t.Fatal(err)
	}

	chat.Messages = []providers.ChatMessage{{Role: "user", Content: "provider-secret"}}

	m.localAgent = localAgentState{
		client:    client,
		chat:      chat,
		chatScope: providers.ChatScope{Profile: "worker"},
		lines:     []string{"You: provider-secret"},
		input:     textField{value: "provider-secret"},
	}
	if result := m.saveLocalConversation()().(localConversationSaved); result.err != nil {
		t.Fatal(result.err)
	}

	loaded, err := providers.LoadConversation(m.localAgent.chatScope, chat.Directory, chat.ID)
	if err != nil ||
		strings.Contains(
			loaded.Title+loaded.Draft+loaded.Lines[0]+loaded.Messages[0].Content,
			"provider-secret",
		) {
		t.Fatalf("credential saved in history: %+v %v", loaded, err)
	}
}

func TestHistoryShortcutAndStaleResults(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	m := dashboardFixture()
	m.localAgent = localAgentState{open: true, sequence: 4}
	next, cmd := m.updateLocalAgent(tea.KeyPressMsg{Code: 'r', Mod: tea.ModCtrl})

	m = next.(model)
	if !m.localAgent.sessions.open || cmd == nil {
		t.Fatal("Ctrl+r did not open chat history")
	}

	oldResult := cmd()
	m.localAgent.sequence++
	next, _ = m.Update(oldResult)

	m = next.(model)
	if !m.localAgent.sessions.busy {
		t.Fatal("old agent history result was accepted")
	}

	next, _ = m.Update(localConversationLoaded{
		sequence:      m.localAgent.sessions.sequence,
		agentSequence: m.localAgent.sequence - 1,
		chat:          providers.Conversation{ID: "old", Directory: "/old/project"},
	})
	if next.(model).localAgent.chat.ID != "" || next.(model).directory == "/old/project" {
		t.Fatal("old agent conversation changed the current project")
	}
}
