package tui

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/rajandhamala/Maruvo/cli/internal/api"
)

func dashboardFixture() model {
	worker := int64(7)
	m := model{ctx: context.Background(), client: api.NewClient("http://localhost"),
		token: "session", user: api.User{ID: "7", GitHubLogin: "developer", GitHubConnected: true},
		width: 120, height: 36, homePath: "~/Desktop/project"}
	m.dashboard = dashboardState{ready: true, all: true, generation: 3,
		feed: []api.Post{
			{
				ID:           10,
				UserID:       8,
				Title:        "Fix flaky tests",
				Level:        "easy",
				Status:       "open",
				CostLamports: 180_000_000,
			},
			{
				ID:           20,
				UserID:       9,
				Title:        "WebSocket reconnect",
				Level:        "medium",
				Status:       "open",
				CostLamports: 420_000_000,
			},
			{
				ID:           30,
				UserID:       8,
				Title:        "Escrow instructions",
				Level:        "complex",
				Status:       "open",
				CostLamports: 900_000_000,
			},
		}, mine: []api.Post{
			{
				ID:         40,
				UserID:     8,
				AcceptedBy: &worker,
				Title:      "API refactor",
				Level:      "medium",
				Status:     "in_progress",
			},
			{ID: 50, UserID: 7, Title: "Document setup", Level: "easy", Status: "open"},
			{ID: 60, UserID: 7, Title: "Review delivery", Level: "complex", Status: "completed"},
		}}
	m.applyDashboardPosts()

	return m
}

func TestDashboardResponsiveLayout(t *testing.T) {
	for _, size := range [][2]int{{48, 16}, {50, 17}, {60, 20}, {80, 24}, {112, 26}, {120, 36}, {168, 44}, {240, 60}} {
		for _, state := range []string{
			"idle", "loading", "error", "profile", "prompt", "search", "empty", "yours",
			"sidebar-hidden", "prompt-hidden", "both-hidden", "hidden-search",
			"filter", "personal-filter", "empty-loading", "empty-error",
		} {
			m := dashboardFixture()
			m.width, m.height = size[0], size[1]

			switch state {
			case "loading":
				m.loading = true
			case "error":
				m.err = errors.New("Wallet belongs to another account; use a separate wallet with --wallet")
			case "profile":
				m.profileOpen = true
			case "prompt":
				m.dashboard.focus = dashboardPrompt
			case "search":
				m.dashboard.focus = dashboardSearch
				m.dashboard.search = textField{value: "Flaky", cursor: 5}
			case "empty":
				m.posts = nil
			case "yours":
				m.own, m.screen = true, myPostsScreen
				m.applyDashboardPosts()
			case "sidebar-hidden":
				m.dashboard.sidebarHidden = true
			case "prompt-hidden":
				m.dashboard.promptHidden = true
			case "both-hidden":
				m.dashboard.sidebarHidden, m.dashboard.promptHidden = true, true
			case "hidden-search":
				m.dashboard.sidebarHidden, m.dashboard.promptHidden = true, true
				m.dashboard.focus = dashboardSearch
				m.dashboard.search = textField{value: "Flaky", cursor: 5}
			case "filter":
				m = m.openDashboardFilter()
			case "personal-filter":
				m.own, m.screen = true, myPostsScreen
				m.applyDashboardPosts()
				m = m.openDashboardFilter()
			case "empty-loading":
				m.posts, m.loading = nil, true
			case "empty-error":
				m.posts, m.dashboard.ready = nil, false
				m.err = errors.New("connection failed")
			}

			view := m.View()

			rows := strings.Split(view.Content, "\n")
			if len(rows) != size[1] || view.BackgroundColor == nil {
				t.Fatalf("%v %s: wrong row count or missing home theme", size, state)
			}

			for _, row := range rows {
				if ansi.StringWidth(row) > size[0] {
					t.Fatalf("%v %s: row overflow: %q", size, state, ansi.Strip(row))
				}
			}

			if state != "profile" &&
				((!m.dashboard.promptHidden && !strings.Contains(view.Content, "Enter send")) ||
					!strings.Contains(view.Content, m.homePath)) {
				t.Fatalf("%v %s: prompt action or path missing", size, state)
			}

			if (state == "search" || state == "hidden-search") &&
				!strings.Contains(ansi.Strip(view.Content), "Flaky") {
				t.Fatalf("%v: search must stay visible in short terminals", size)
			}

			l := m.dashboardLayout()
			if m.dashboard.filterOpen {
				area := m.dashboardFilterArea()
				if area.x < 0 || area.x+area.width > m.dashboardGeometry().mainWidth ||
					area.y+area.height > m.bodyHeight()-3 {
					t.Fatalf("%v %s: dropdown outside usable task space", size, state)
				}
			}

			if m.dashboard.promptHidden && strings.Contains(view.Content, "Enter send") {
				t.Fatalf("%v %s: hidden prompt still rendered", size, state)
			}

			for _, hit := range l.hits {
				if hit.x < 0 || hit.y < 0 || hit.x+hit.width > m.contentWidth() ||
					hit.y+hit.height > m.bodyHeight() {
					t.Fatalf("%v %s: inaccessible mouse target: %+v", size, state, hit)
				}
			}
		}
	}
}

func TestDashboardPanelToggles(t *testing.T) {
	m := dashboardFixture()
	m.selected = 1
	m.homeInput = textField{value: "Keep this draft", cursor: 15}
	m.dashboard.focus = dashboardPrompt

	before := m.dashboardGeometry()
	if before.sideWidth == 0 {
		t.Fatal("wide terminals should initially show the personal panel")
	}

	next, cmd := m.Update(tea.KeyPressMsg{Code: 'b', Mod: tea.ModCtrl})

	m = next.(model)
	if cmd != nil || m.dashboardGeometry().sideWidth != 0 ||
		m.dashboardGeometry().mainWidth <= before.mainWidth || m.homeInput.value != "Keep this draft" ||
		m.selected != 1 {
		t.Fatal("Ctrl+b should return sidebar space to the main view and retain the draft")
	}

	next, _ = m.Update(tea.KeyPressMsg{Code: 't', Mod: tea.ModCtrl})

	m = next.(model)
	if !m.dashboard.promptHidden || m.dashboard.focus != dashboardTasks ||
		m.dashboardGeometry().listHeight != before.listHeight+homePromptHeight ||
		m.homeInput.value != "Keep this draft" {
		t.Fatal("Ctrl+t should hide the prompt, return focus to tasks, and retain its text")
	}

	for _, hit := range m.dashboardLayout().hits {
		if hit.action == "dashboard-prompt" || hit.action == "dashboard-submit" ||
			hit.action == "dashboard-task" {
			t.Fatal("hidden panels must not leave mouse targets behind")
		}
	}

	next, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyTab})

	m = next.(model)
	if m.dashboard.promptHidden || m.dashboard.focus != dashboardPrompt ||
		m.homeInput.value != "Keep this draft" {
		t.Fatal("Tab should restore and focus the saved prompt")
	}

	next, _ = m.Update(tea.KeyPressMsg{Code: 'b', Mod: tea.ModCtrl})

	m = next.(model)
	if m.dashboardGeometry().sideWidth != before.sideWidth {
		t.Fatal("Ctrl+b should restore the personal panel while editing a draft")
	}

	next, _ = m.Update(tea.KeyPressMsg{Code: 't', Mod: tea.ModCtrl})
	next, _ = next.Update(tea.KeyPressMsg{Code: 't', Mod: tea.ModCtrl})

	m = next.(model)
	if m.dashboard.promptHidden || m.dashboard.focus != dashboardPrompt ||
		m.homeInput.value != "Keep this draft" {
		t.Fatal("Ctrl+t should restore the saved prompt and focus it")
	}

	for _, size := range [][2]int{{48, 16}, {168, 44}} {
		next, _ = m.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})

		m = next.(model)
		if m.dashboard.sidebarHidden || m.dashboard.promptHidden {
			t.Fatal("automatic resizing must preserve panel preferences")
		}
	}
}

func TestDashboardSidebarDoesNotScrollFeed(t *testing.T) {
	m := dashboardFixture()
	g := m.dashboardGeometry()

	next, _ := m.updateWheel(tea.MouseWheelMsg{X: m.contentX() + g.sideX + 2,
		Y: m.bodyStart() + 6, Button: tea.MouseWheelDown})
	if next.(model).selected != m.selected {
		t.Fatal("scrolling over personal information must not move the feed selection")
	}
}

func TestDashboardPromptDraftAndShortcuts(t *testing.T) {
	m := dashboardFixture()
	m.homeInput = textField{value: "Keep the login draft", cursor: 20, limit: 2000}
	next, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyTab})

	m = next.(model)
	if m.dashboard.focus != dashboardPrompt {
		t.Fatal("Tab must focus the prompt")
	}

	for _, char := range " qrnogw123/" {
		next, cmd := m.Update(tea.KeyPressMsg{Code: char, Text: string(char)})

		m = next.(model)
		if cmd != nil || !m.onDashboard() || m.dashboard.focus != dashboardPrompt {
			t.Fatal("ordinary prompt text must not run dashboard shortcuts")
		}
	}

	next, _ = m.Update(tea.PasteMsg{Content: "\x1b[31m hello\nworld\x1b[0m"})
	m = next.(model)

	draft := m.homeInput.value
	if strings.ContainsAny(draft, "\x1b\n") || !strings.Contains(draft, "hello world") {
		t.Fatal("prompt paste must preserve safe text")
	}

	next, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})

	m = next.(model)
	if cmd == nil || !m.onDashboard() || !m.localAgent.open || !m.localAgent.autoSend ||
		m.localAgent.input.value != draft || m.homeInput.value != draft {
		t.Fatal("Enter must queue the prompt for the agent and retain it while the model loads")
	}

	next, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})

	m = next.(model)
	if !m.onDashboard() || m.homeInput.value != draft {
		t.Fatal("cancel must retain the prompt draft")
	}
}

func TestDashboardFilterSearchAndMouse(t *testing.T) {
	m := dashboardFixture()
	next, cmd := m.updateDashboard(tea.KeyPressMsg{Code: tea.KeyRight})

	m = next.(model)
	if cmd != nil || m.dashboard.all || !slices.Equal(m.listIndices(), []int{0}) {
		t.Fatal("difficulty filtering should use the loaded snapshot")
	}

	next, _ = m.updateDashboard(tea.KeyPressMsg{Code: tea.KeyLeft})
	m = next.(model)
	next, _ = m.Update(tea.KeyPressMsg{Code: 'f', Mod: tea.ModCtrl})
	next, _ = next.Update(tea.PasteMsg{Content: "RECONNECT"})

	m = next.(model)
	if !slices.Equal(m.listIndices(), []int{1}) || m.selected != 1 {
		t.Fatal("search must preserve original task indices and select a match")
	}

	for _, hit := range m.dashboardLayout().hits {
		if hit.action == "post" {
			next, cmd = m.updateMouse(tea.MouseClickMsg{X: m.contentX() + hit.x,
				Y: m.bodyStart() + hit.y, Button: tea.MouseLeft})

			m = next.(model)
			if cmd == nil || m.screen != detailScreen || m.posts[m.selected].ID != 20 {
				t.Fatal("click must open the visible search result")
			}

			return
		}
	}

	t.Fatal("search result missing mouse target")
}

func TestDashboardSecondaryActions(t *testing.T) {
	for _, action := range []string{"dashboard-task", "dashboard-prompt", "dashboard-submit"} {
		m := dashboardFixture()
		m.homeInput = textField{value: "Dashboard task", cursor: 14, limit: 2000}
		found := false

		for _, hit := range m.dashboardLayout().hits {
			if hit.action != action {
				continue
			}

			found = true
			next, cmd := m.updateMouse(tea.MouseClickMsg{X: m.contentX() + hit.x,
				Y: m.bodyStart() + hit.y, Button: tea.MouseLeft})
			m = next.(model)

			switch action {
			case "dashboard-task":
				if cmd == nil || m.screen != detailScreen || !m.own || m.posts[m.selected].ID != 40 {
					t.Fatal("sidebar must open the own task, independent of the feed index")
				}
			case "dashboard-prompt":
				if m.dashboard.focus != dashboardPrompt || cmd != nil {
					t.Fatal("prompt padding must accept focus without submitting")
				}
			case "dashboard-submit":
				if cmd == nil || !m.localAgent.open || !m.localAgent.autoSend ||
					m.localAgent.input.value != "Dashboard task" || !m.onDashboard() {
					t.Fatal("prompt button must send the typed request to the agent")
				}
			}

			break
		}

		if !found {
			t.Fatalf("missing %s", action)
		}
	}
}

func TestDashboardRefreshAndStaleResults(t *testing.T) {
	m := dashboardFixture()
	m.selected = 1
	m.homeInput = textField{value: "Keep draft", cursor: 10}
	result := dashboardResult{generation: 3, feed: []api.Post{m.posts[2], m.posts[1], m.posts[0]},
		mine: m.dashboard.mine}
	next, _ := m.Update(result)

	m = next.(model)
	if m.posts[m.selected].ID != 20 || m.homeInput.value != "Keep draft" {
		t.Fatal("refresh must preserve task selection by ID and the prompt draft")
	}

	next, _ = m.Update(dashboardResult{generation: 3, err: errors.New("connection failed")})

	m = next.(model)
	if len(m.posts) != 3 || len(m.dashboard.mine) != 3 || m.err == nil {
		t.Fatal("failed refresh must retain the last successful snapshot")
	}

	m.dashboard.generation++
	m.loading = true

	next, _ = m.Update(result)
	if !next.(model).loading {
		t.Fatal("stale results must not finish a newer request")
	}

	m.loading = false
	next, _ = m.Update(logoutResult{})
	next, _ = next.Update(result)

	m = next.(model)
	if m.token != "" || m.dashboard.ready || len(m.dashboard.mine) != 0 || len(m.posts) != 0 {
		t.Fatal("late dashboard results must not restore a signed-out account")
	}
}

func TestDashboardFetchesRealTaskSources(t *testing.T) {
	var calls atomic.Int32

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.Header.Get("Authorization") != "Bearer session" {
			t.Error("dashboard must use authenticated read-only task requests")
		}

		calls.Add(1)

		posts := []api.Post{}

		switch r.URL.Path {
		case "/posts/feed":
			var input struct{ Level string }
			if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
				t.Error(err)
			}

			index := slices.Index(levels, input.Level)
			if index < 0 {
				t.Error("unexpected level")
				return
			}

			posts = []api.Post{{ID: int64(index + 1), Level: input.Level,
				CreatedAt: time.Unix(int64(index+1), 0)}}
		case "/posts/urs":
			posts = []api.Post{{ID: 9, UpdatedAt: time.Unix(1, 0)}, {ID: 8, UpdatedAt: time.Unix(2, 0)}}
		default:
			t.Errorf("unexpected API call: %s", r.URL.Path)
		}

		if err := json.NewEncoder(w).Encode(map[string]any{"posts": posts}); err != nil {
			t.Error(err)
		}
	}))
	defer server.Close()

	m := dashboardFixture()
	m.client = api.NewClient(server.URL)

	result := m.fetchDashboard()().(dashboardResult)
	if result.err != nil || calls.Load() != 4 || len(result.feed) != 3 ||
		result.feed[0].ID != 3 || result.mine[0].ID != 8 || result.generation != 3 {
		t.Fatal("dashboard must combine all difficulty levels and sort real task updates")
	}
}

func TestDashboardUnauthorizedClearsAccountData(t *testing.T) {
	m := dashboardFixture()
	m.walletAddress = "linked-wallet"
	next, cmd := m.Update(dashboardResult{generation: 3,
		err: &api.Error{StatusCode: http.StatusUnauthorized, Message: "expired"}})

	m = next.(model)
	if cmd == nil || m.token == "" {
		t.Fatal("task errors must recheck the current session before clearing it")
	}

	next, _ = m.Update(sessionChecked{token: m.token, err: &api.Error{StatusCode: http.StatusUnauthorized}})

	m = next.(model)
	if m.token != "" || m.walletAddress != "" || len(m.posts) != 0 || len(m.dashboard.mine) != 0 {
		t.Fatal("expired sessions must clear dashboard and wallet data")
	}
}

func TestDashboardMouseBoundaries(t *testing.T) {
	for _, size := range [][2]int{{48, 16}, {80, 24}, {168, 44}} {
		m := dashboardFixture()
		m.width, m.height = size[0], size[1]
		m.homeInput = textField{value: "hello are u here?", cursor: 17, limit: 2000}
		m.user.GitHubLogin = strings.Repeat("developer", 20)
		area := m.profileArea()

		next, _ := m.updateMouse(tea.MouseClickMsg{X: area.x + area.width - 1, Y: 1, Button: tea.MouseLeft})
		if !next.(model).profileOpen {
			t.Fatal("the visible profile arrow must open the menu")
		}

		for _, hit := range m.dashboardLayout().hits {
			if hit.action == "dashboard-submit" {
				for x := hit.x; x < hit.x+hit.width; x++ {
					next, cmd := m.updateMouse(tea.MouseClickMsg{X: m.contentX() + x,
						Y: m.bodyStart() + hit.y, Button: tea.MouseLeft})
					if cmd == nil || !next.(model).localAgent.open || !next.(model).onDashboard() {
						t.Fatal("every cell of Enter send must open the agent with the prompt")
					}
				}
			}
		}

		m.selected = len(m.posts) - 1

		next, _ = m.updateWheel(
			tea.MouseWheelMsg{X: m.contentX(), Y: m.bodyStart() + 2, Button: tea.MouseWheelUp},
		)
		if next.(model).selected != 1 {
			t.Fatal("wheel up must move exactly one task at the end of the list")
		}

		for _, hit := range m.dashboardLayout().hits {
			if hit.action == "dashboard-prompt" {
				next, _ = m.updateWheel(tea.MouseWheelMsg{X: m.contentX() + hit.x,
					Y: m.bodyStart() + hit.y, Button: tea.MouseWheelUp})
				if next.(model).selected != m.selected {
					t.Fatal("scrolling over the prompt must not navigate tasks")
				}
			}
		}
	}
}

func TestDashboardPublishedDraft(t *testing.T) {
	m := dashboardFixture()
	m.screen, m.form = newPostScreen, newPostForm()
	m.homeInput = textField{value: "New task", cursor: 8}
	m.dashboard.search = textField{value: "old search", cursor: 10}

	next, _ := m.Update(postChanged{err: errors.New("creation failed")})
	if next.(model).homeInput.value != "New task" {
		t.Fatal("failed creation must retain the home draft")
	}

	next, cmd := m.Update(postChanged{created: true, post: api.Post{ID: 70}})

	m = next.(model)
	if cmd == nil || !m.own || m.screen != myPostsScreen || m.taskFilter != 1 ||
		m.homeInput.value != "" || m.dashboard.search.value != "" {
		t.Fatal("successful creation must return to created tasks with no stale search or draft")
	}
}
