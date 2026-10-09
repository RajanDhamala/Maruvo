package tui

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/rajandhamala/Maruvo/cli/internal/api"
	"github.com/rajandhamala/Maruvo/cli/internal/providers"
)

func TestActionUnauthorizedRechecksSession(t *testing.T) {
	for _, status := range []int{200, 401, 503} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/me" || r.Header.Get("Authorization") != "Bearer session" {
					t.Error("wrong session check")
				}

				w.WriteHeader(status)
				_, _ = w.Write([]byte(`{"id":"7","username":"developer"}`))
			}))
			defer server.Close()

			m := dashboardFixture()
			m.client = api.NewClient(server.URL)
			next, cmd := m.Update(
				dashboardResult{generation: 3, err: &api.Error{StatusCode: 401, Message: "action failed"}},
			)

			m = next.(model)
			if cmd == nil || m.token != "session" {
				t.Fatal("session cleared without revalidation")
			}

			next, _ = m.Update(cmd())

			m = next.(model)
			if (m.token == "") != (status == 401) {
				t.Fatal("only a confirmed expired session may clear authentication")
			}
		})
	}
}

func TestStaleAuthenticationCannotDiscardNewLogin(t *testing.T) {
	m := dashboardFixture()
	for _, msg := range []tea.Msg{
		sessionChecked{token: "old", err: &api.Error{StatusCode: 401}},
		authResult{restored: true, previousToken: "old", err: errors.New("unavailable")},
	} {
		next, _ := m.Update(msg)
		if next.(model).token != "session" {
			t.Fatal("stale auth response discarded current login")
		}
	}
}

func TestRestoredSessionFocusesPrompt(t *testing.T) {
	for _, setupFirst := range []bool{false, true} {
		m := dashboardFixture()
		m.token = ""
		if setupFirst {
			next, _ := m.Update(startupProviderLoaded{})
			m = next.(model)
		}
		next, _ := m.Update(authResult{restored: true, token: "session", user: api.User{ID: "7"}})
		m = next.(model)
		if !setupFirst {
			next, _ = m.Update(startupProviderLoaded{})
			m = next.(model)
		}
		if m.dashboard.focus != dashboardPrompt || m.dashboard.promptHidden || !m.providers.open {
			t.Fatal("startup must preserve provider setup and prompt focus in either load order")
		}
		next, _ = m.closeProviders()
		m = next.(model)
		m.loading = false
		next, _ = m.Update(tea.KeyPressMsg{Code: 'h', Text: "h"})
		if next.(model).homeInput.value != "h" {
			t.Fatal("first key did not reach the prompt")
		}
	}
}

func TestAgentStopKeepsConversationAndUsageAtBottom(t *testing.T) {
	m := dashboardFixture()
	ctx, cancel := context.WithCancel(context.Background())
	m.localAgent = localAgentState{
		open:     true,
		busy:     true,
		sequence: 1,
		client:   &providers.Client{},
		ctx:      ctx,
		cancel:   cancel,
		lines: []string{
			"Agent: **Ready** to help.",
		},
		history: []providers.Message{{Role: "assistant", Content: "Ready"}},
		usage:   "Usage (API): input 12 · output 8",
	}
	next, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})

	m = next.(model)
	if ctx.Err() == nil || !m.localAgent.open || m.localAgent.busy || len(m.localAgent.history) != 1 {
		t.Fatal("Esc should stop a running request while retaining the agent conversation")
	}

	for _, size := range [][2]int{{48, 16}, {80, 24}, {120, 36}} {
		m.width, m.height = size[0], size[1]

		view := ansi.Strip(m.View().Content)
		if !strings.Contains(view, "Ready") || strings.Contains(view, "**Ready**") ||
			strings.Index(view, "Usage (API)") < strings.Index(view, "Ready") ||
			strings.Index(view, "Enter send") < strings.Index(view, "Usage (API)") {
			t.Fatal("assistant Markdown should render cleanly with usage above the bottom input")
		}
	}
}
