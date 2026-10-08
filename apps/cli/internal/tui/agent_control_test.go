package tui

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/rajandhamala/Maruvo/cli/internal/api"
)

func TestHumanControlsConfirmTakeoverAndPreserveWorkspaceDraft(t *testing.T) {
	var mutations atomic.Int32

	mode := "agent"
	now := time.Now()
	grant := api.AgentGrant{ID: "owned-grant", Name: "My harness", Permissions: []string{"read", "message"},
		ExpiresAt: now.Add(time.Hour)}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer session" {
			t.Error("control did not use the human login")
		}

		switch r.URL.Path {
		case "/posts/7/agent-control":
			if r.Method == http.MethodPut {
				var payload struct {
					Mode string `json:"mode"`
				}

				_ = json.NewDecoder(r.Body).Decode(&payload)
				mode = payload.Mode

				mutations.Add(1)

				grant.RevokedAt = &now
			}

			_ = json.NewEncoder(w).
				Encode(api.AgentControl{PostID: 7, OwnerID: 7, Mode: mode, UpdatedAt: &now})
		case "/posts/7/agents":
			_ = json.NewEncoder(w).Encode([]api.AgentGrant{grant})
		default:
			t.Errorf("controls reached unrelated action: %s", r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	defer server.Close()

	m := dashboardFixture()
	m.client, m.screen, m.workspaceCtx = api.NewClient(server.URL), workspaceScreen, m.ctx
	m.workspace.Post = api.Post{ID: 7, UserID: 7}
	m.composer.draft = textField{value: "Unsaved human answer", limit: 4000}
	m.composer.attachments = []localFile{{path: "input.txt"}}
	next, cmd := m.Update(tea.KeyPressMsg{Code: 't', Mod: tea.ModCtrl})
	m = next.(model)
	next, _ = m.Update(cmd())
	m = next.(model)

	if !m.agentControls.open || len(m.agentControls.grants) != 1 {
		t.Fatal("controls did not load owned grants")
	}

	for _, size := range [][2]int{{48, 16}, {80, 24}, {120, 36}} {
		m.width, m.height = size[0], size[1]

		view := ansi.Strip(m.View().Content)
		if !strings.Contains(view, "Permissions:") || !strings.Contains(view, "Expires:") {
			t.Fatalf("grant details hidden at %v", size)
		}

		for _, line := range strings.Split(view, "\n") {
			if ansi.StringWidth(line) > size[0] {
				t.Fatalf("controls overflow at %v", size)
			}
		}
	}

	next, cmd = m.Update(tea.KeyPressMsg{Code: 'm'})

	m = next.(model)
	if cmd != nil || mutations.Load() != 0 || m.agentControls.confirm != "manual" {
		t.Fatal("takeover ran before confirmation")
	}

	next, cmd = m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = next.(model)
	next, _ = m.Update(cmd())

	m = next.(model)
	if mutations.Load() != 1 || m.workspace.AgentControl.Mode != "manual" ||
		m.agentControls.grants[0].RevokedAt == nil {
		t.Fatal("manual mode or revoked access missing")
	}

	next, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})

	m = next.(model)
	if m.agentControls.open || m.composer.draft.value != "Unsaved human answer" ||
		len(m.composer.attachments) != 1 {
		t.Fatal("closing controls lost chat draft or attachment")
	}
}

func TestControlUpdatesAreOwnedAndCannotReplaceNewerState(t *testing.T) {
	m := dashboardFixture()
	m.screen, m.workspaceGen = workspaceScreen, 4
	m.workspace.Post.ID = 7
	now := time.Now()
	m.workspace.AgentControl = api.AgentControl{PostID: 7, OwnerID: 7, Mode: "manual", UpdatedAt: &now}
	m.agentControls = agentControls{open: true, generation: 2}
	older := now.Add(-time.Minute)

	next, cmd := m.Update(agentControlsLoaded{generation: 2, workspaceGen: 4, postID: 7, token: m.token,
		control: api.AgentControl{PostID: 7, OwnerID: 7, Mode: "agent", UpdatedAt: &older}})

	m = next.(model)
	if m.workspace.AgentControl.Mode != "manual" || cmd == nil {
		t.Fatal("old response replaced a newer takeover")
	}

	next, cmd = m.Update(agentControlsLoaded{generation: 2, workspaceGen: 3, postID: 7, token: m.token,
		control: api.AgentControl{Mode: "agent"}})

	m = next.(model)
	if cmd != nil || m.workspace.AgentControl.Mode != "manual" {
		t.Fatal("previous workspace response changed current control")
	}

	data, _ := json.Marshal(api.AgentControl{PostID: 7, OwnerID: 8, Mode: "agent"})
	m.applyWorkspaceEvent(api.WorkspaceEvent{PostID: 7, ID: 1, Kind: "agent.control", Data: data})

	if m.workspace.AgentControl.Mode != "manual" {
		t.Fatal("other participant changed this owner's control")
	}

	owner := int64(7)
	m.workspace.Events = append(
		m.workspace.Events,
		api.WorkspaceEvent{PostID: 7, ActorID: &owner, Kind: "message",
			Data: []byte(`{"text":"Agent answer","agent_name":"My harness"}`)},
	)
	rows := m.conversationRows(80)

	found := false
	for _, row := range rows {
		found = found || strings.Contains(ansi.Strip(row.text), "You · agent My harness")
	}

	if !found {
		t.Fatal("agent message appeared as a human message")
	}

	label := m.eventLabel(api.WorkspaceEvent{ActorID: &owner, Kind: "work.submitted",
		Data: []byte(`{"note":"Ready","agent_name":"My harness"}`)})
	if !strings.Contains(label, "You · agent My harness submitted work") {
		t.Fatal("agent delivery appeared as a human action")
	}
}
