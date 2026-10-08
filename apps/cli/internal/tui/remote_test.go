package tui

import (
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/rajandhamala/Maruvo/cli/internal/api"
)

func TestRemotePickerTargetsOfflineSellerAndPreservesPrompt(t *testing.T) {
	m := dashboardFixture()
	m.homeInput = textField{value: "Keep this draft", limit: 2000}
	next, _ := m.openRemote()
	m = next.(model)
	next, _ = m.Update(remoteLoaded{
		generation: m.remote.generation,
		token:      m.token,
		offers: []api.AgentOffer{
			{UserID: 9, OfferTerms: api.OfferTerms{Name: "Remote Go worker", MinLamports: 100}},
		},
	})

	m = next.(model)
	for _, size := range [][2]int{{48, 16}, {80, 24}, {120, 36}} {
		m.width, m.height = size[0], size[1]

		view := ansi.Strip(m.View().Content)
		if !strings.Contains(view, "offline") {
			t.Fatalf("offline seller hidden at %v", size)
		}

		for _, line := range strings.Split(view, "\n") {
			if ansi.StringWidth(line) > size[0] {
				t.Fatalf("remote view overflows at %v", size)
			}
		}
	}

	next, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})

	m = next.(model)
	if m.screen != newPostScreen || m.form.targetWorker == nil || *m.form.targetWorker != 9 ||
		m.form.fields[1].value != "100" ||
		m.homeInput.value != "Keep this draft" {
		t.Fatal("delegation lost seller terms or prompt")
	}

	m.form.fields[0].value = "Fix sorting"
	m.form.fields[3].value = "Sort numerically"
	m.form.descriptionSource = descriptionFromText
	m.form.timings[1].value = time.Now().Add(72 * time.Hour).Format("2006-01-02 15:04")

	payload, err := m.form.payload()
	if err != nil || payload.TargetWorker == nil || *payload.TargetWorker != 9 {
		t.Fatalf("form dropped directed target: %+v %v", payload, err)
	}
}

func TestRemoteStatusRefreshCannotReplaceNewerEventOrChatDraft(t *testing.T) {
	m := dashboardFixture()
	m.screen = workspaceScreen
	m.workspaceGen = 3
	m.workspace = api.Workspace{
		Post:   api.Post{ID: 7, Remote: api.RemoteStatus{Status: "working"}},
		Cursor: "2-0",
	}
	m.composer.draft = textField{value: "Unsaved answer", limit: 4000}
	next, _ := m.Update(
		remoteStatusLoaded{
			generation: 3,
			cursor:     "1-0",
			post:       api.Post{ID: 7, Remote: api.RemoteStatus{Status: "interrupted"}},
		},
	)

	m = next.(model)
	if m.workspace.Post.Remote.Status != "working" {
		t.Fatal("stale status replaced a newer event")
	}

	next, _ = m.Update(
		remoteStatusLoaded{
			generation: 3,
			cursor:     "2-0",
			post:       api.Post{ID: 7, Remote: api.RemoteStatus{Status: "interrupted"}},
		},
	)

	m = next.(model)
	if m.workspace.Post.Remote.Status != "interrupted" || m.composer.draft.value != "Unsaved answer" {
		t.Fatal("status refresh lost chat draft")
	}

	worker := int64(2)
	m.workspace.Post.AcceptedBy = &worker
	m.workspace.Escrow.State = "confirmed"
	m.applyWorkspaceEvent(
		api.WorkspaceEvent{
			PostID:    7,
			ID:        1,
			StreamID:  "3-0",
			Kind:      "agent.activity",
			CreatedAt: time.Now(),
			Data:      []byte(`{"state":"waiting_for_answer","detail":"Which file?"}`),
		},
	)

	if m.workspace.Post.Remote.Status != "waiting_for_answer" ||
		m.workspace.Post.Remote.Detail != "Which file?" {
		t.Fatal("clarification activity missing")
	}
}
