package tui

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/rajandhamala/Maruvo/cli/internal/api"
)

func chatModel() model {
	return model{
		screen:       workspaceScreen,
		workspaceGen: 7,
		width:        80,
		height:       24,
		composer:     chatComposer{root: ".", draft: textField{limit: 4000}},
		workspace: api.Workspace{
			Post:  api.Post{ID: 1, Status: "in_progress"},
			State: api.WorkspaceState{ReviewState: "working"},
		},
	}
}

func TestChatRetryKeepsMessageID(t *testing.T) {
	var (
		ids []string
		mu  sync.Mutex
	)

	snapshot := func() []string {
		mu.Lock()
		defer mu.Unlock()

		return append([]string(nil), ids...)
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload map[string]string
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Error(err)
		}

		mu.Lock()

		ids = append(ids, payload["message_id"])
		fail := len(ids) <= 2
		mu.Unlock()

		if fail {
			http.Error(w, "temporarily unavailable", http.StatusServiceUnavailable)
			return
		}

		w.WriteHeader(http.StatusCreated)
	}))
	defer server.Close()

	m := chatModel()
	m.client, m.workspaceCtx = api.NewClient(server.URL), context.Background()
	m.composer.draft.value = "hello"
	next, command := m.sendChat()
	m = next.(model)

	result := command().(chatSent)
	if result.err == nil {
		t.Fatal("expected the first send to fail")
	}

	next, _ = m.chatSent(result)
	m = next.(model)
	next, command = m.sendChat()
	m = next.(model)
	result = command().(chatSent)

	got := snapshot()
	if result.err != nil || len(got) != 3 || got[0] == "" || got[0] != got[1] || got[0] != got[2] {
		t.Fatalf("manual retry changed message ID: ids=%v, err=%v", got, result.err)
	}

	next, _ = m.chatSent(result)
	m = next.(model)
	m.composer.draft.value = "hello"
	next, command = m.sendChat()
	m = next.(model)
	result = command().(chatSent)

	got = snapshot()
	if result.err != nil || len(got) != 4 || got[3] == got[0] {
		t.Fatal("a new draft reused the successfully sent message ID")
	}

	next, _ = m.chatSent(chatSent{generation: m.workspaceGen, err: errors.New("lost response")})
	m = next.(model)
	m.composer.draft.value = "edited"
	_, command = m.sendChat()
	result = command().(chatSent)

	got = snapshot()
	if result.err != nil || len(got) != 5 || got[4] == got[3] {
		t.Fatal("editing a failed draft did not create a new message ID")
	}
}

func TestChatTypingDoesNotTriggerNavigation(t *testing.T) {
	m := chatModel()
	for _, char := range "muvbs123q" {
		next, cmd := m.updateWorkspace(tea.KeyPressMsg{Code: char, Text: string(char)})

		m = next.(model)
		if cmd != nil || !m.composing() {
			t.Fatalf("typing %q triggered a command", char)
		}
	}

	if m.composer.draft.value != "muvbs123q" {
		t.Fatal("ordinary text must stay in the composer")
	}
}

func TestFileCompletionFindsNestedFilesAndExcludesGeneratedDirectories(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"src/a/report final.txt", "src/b/report.txt", "node_modules/report.txt", ".git/report.txt", "target/report.txt"} {
		path := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}

		if err := os.WriteFile(path, []byte("content"), 0600); err != nil {
			t.Fatal(err)
		}
	}

	files, err := localFileSuggestions(root, "report")
	if err != nil || len(files) != 2 {
		t.Fatalf("expected two project files, got %v, %v", files, err)
	}

	m := chatModel()
	m.composer.root = root
	m.composer.draft = textField{
		value:  "Please inspect @report today",
		cursor: len("Please inspect @report"),
		limit:  4000,
	}
	m.composer.suggestions, m.composer.completing = files, true
	next, _ := m.attachSuggestion()

	m = next.(model)
	if len(m.composer.attachments) != 1 || m.composer.draft.value != "Please inspect  today" || m.loading {
		t.Fatal("selecting a file must attach it to the unsent draft and preserve other text")
	}

	if _, _, _, active := attachmentToken(textField{value: "mail@report", cursor: 11}); active {
		t.Fatal("email addresses must not trigger file completion")
	}
}

func TestStaleFileCompletionIsIgnored(t *testing.T) {
	m := chatModel()

	m.composer.sequence, m.composer.completing = 4, true
	for _, msg := range []localFilesFound{
		{generation: 6, sequence: 4, files: []localFile{{label: "old workspace"}}},
		{generation: 7, sequence: 3, files: []localFile{{label: "old query"}}},
	} {
		next, _ := m.Update(msg)

		m = next.(model)
		if len(m.composer.suggestions) != 0 {
			t.Fatal("stale file search must not replace current suggestions")
		}
	}
}

func TestPartialSendKeepsDraftAndOnlyUnsentAttachments(t *testing.T) {
	m := chatModel()
	m.loading = true
	m.composer.draft = textField{value: "Please review", cursor: 13, limit: 4000}
	m.composer.attachments = []localFile{{path: "first.txt"}, {path: "second.txt"}}
	next, _ := m.chatSent(
		chatSent{
			generation: 7,
			uploaded:   []api.WorkspaceFile{{ID: "first", Name: "first.txt"}},
			err:        errors.New("connection lost"),
		},
	)

	m = next.(model)
	if m.loading || m.err == nil || m.composer.draft.value != "Please review" ||
		len(m.composer.attachments) != 1 ||
		m.composer.attachments[0].path != "second.txt" {
		t.Fatal("a failed send must keep the text and the files that were not sent")
	}

	m.applyWorkspaceEvent(
		api.WorkspaceEvent{
			ID:     1,
			PostID: 1,
			Kind:   "file.shared",
			Data:   json.RawMessage(`{"id":"first","name":"first.txt","size":10}`),
		},
	)

	if len(m.workspace.Files) != 1 {
		t.Fatal("upload acknowledgement and live event must not duplicate a file")
	}
}

func TestComposerFitsTerminalAndStaysAtBottom(t *testing.T) {
	for _, size := range [][2]int{{48, 16}, {80, 24}, {110, 38}, {180, 50}} {
		m := chatModel()
		m.width, m.height = size[0], size[1]
		m.workspace.AgentControl.Mode = "manual"
		m.workspace.Post.Remote.Status = "working"
		m.composer.attachments = []localFile{{path: "report final.txt"}}
		m.composer.draft = textField{value: strings.Repeat("long draft ", 30), cursor: 330, limit: 4000}
		m.composer.completing, m.composer.suggestions = true, []localFile{{label: "src/report.txt"}}

		layout := m.chatLayout()
		if len(layout.rows) != m.bodyHeight() {
			t.Fatalf("%dx%d: got %d rows, available %d", m.width, m.height, len(layout.rows), m.bodyHeight())
		}

		last := ansi.Strip(layout.rows[len(layout.rows)-1])
		if last != strings.Repeat("─", m.contentWidth()) {
			t.Fatal("composer must remain anchored at the bottom")
		}

		if m.contentX() != 2 {
			t.Fatal("workspace must use the available terminal width")
		}
	}
}

func TestSlashFilesAndEscapePreserveUnsentText(t *testing.T) {
	m := chatModel()
	m.composer.draft = textField{value: "/files", cursor: 6, limit: 4000}
	next, _ := m.updateWorkspace(tea.KeyPressMsg{Code: tea.KeyEnter})

	m = next.(model)
	if !m.workspaceFiles || m.composer.draft.value != "" {
		t.Fatal("/files must open received files")
	}

	m.composer.draft = textField{value: "Unsent", cursor: 6, limit: 4000}
	next, _ = m.updateWorkspace(tea.KeyPressMsg{Code: tea.KeyEsc})

	m = next.(model)
	if !m.composing() || m.composer.draft.value != "Unsent" {
		t.Fatal("returning from files must preserve the draft")
	}
}

func TestPastePreservesLinesAndStripsTerminalControls(t *testing.T) {
	m := chatModel()
	m.token = "fixture"
	next, _ := m.Update(tea.PasteMsg{Content: "Please check:\n\x1b[31mthe patch\x1b[0m"})

	m = next.(model)
	if m.composer.draft.value != "Please check:\nthe patch" {
		t.Fatal("pasted chat must preserve lines without terminal escape sequences")
	}
}
