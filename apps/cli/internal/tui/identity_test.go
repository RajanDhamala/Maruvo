package tui

import (
	"context"
	"errors"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/rajandhamala/Maruvo/cli/internal/api"
)

func TestGitHubConnectionActions(t *testing.T) {
	for _, size := range [][2]int{{48, 16}, {80, 24}, {120, 36}} {
		m := model{ctx: context.Background(), client: api.NewClient("http://localhost"),
			width: size[0], height: size[1], token: "google-session",
			user: api.User{ID: "1", Username: "Google Owner", GoogleConnected: true}}

		if !strings.Contains(ansi.Strip(m.View().Content), "Connect GitHub to your profile: Ctrl+g") {
			t.Fatal("Google users need a visible GitHub connection shortcut")
		}

		updated, cmd := m.Update(tea.KeyPressMsg{Code: 'g', Mod: tea.ModCtrl})
		if next := updated.(model); cmd == nil || !next.loggingIn || next.token != m.token {
			t.Fatal("Ctrl+g must connect GitHub using the current Maruvo session")
		}

		m.screen, m.form = newPostScreen, newPostForm()
		m.form.fields[0].insert("Keep this draft")

		updated, cmd = m.Update(tea.KeyPressMsg{Code: 'g', Mod: tea.ModCtrl})
		if next := updated.(model); cmd == nil || next.form.fields[0].value != "Keep this draft" {
			t.Fatal("connecting GitHub must retain the task draft")
		}

		m.user.GitHubConnected, m.user.GitHubLogin = true, "developer"
		m.screen, m.profileOpen = feedScreen, true

		view := ansi.Strip(m.View().Content)
		if !strings.Contains(view, "Sign-in: Google + GitHub") ||
			!strings.Contains(view, "GitHub connected") ||
			strings.Contains(view, "Connect GitHub") ||
			strings.Contains(view, "g connect GitHub") {
			t.Fatal("connected accounts must show both sign-in methods without a connection action")
		}

		updated, cmd = m.Update(tea.KeyPressMsg{Code: 'g', Mod: tea.ModCtrl})
		if next := updated.(model); cmd != nil || next.loggingIn {
			t.Fatal("GitHub users should not need to reconnect GitHub")
		}

		menu := m.profileMenuArea()

		updated, cmd = m.Update(tea.MouseClickMsg{X: menu.x + 2, Y: menu.y + 5, Button: tea.MouseLeft})
		if next := updated.(model); cmd == nil || !next.loading || next.profileOpen {
			t.Fatal("the account status row must not displace the mouse logout action")
		}
	}
}

func TestSignInExplainsGitHubConnection(t *testing.T) {
	for _, provider := range []string{"github", "google"} {
		m := model{width: 48, height: 16, authProvider: provider}

		view := ansi.Strip(m.View().Content)
		if !strings.Contains(view, "Sign in with GitHub") || !strings.Contains(view, "Sign in with Google") {
			t.Fatal("both sign-in providers must remain visible")
		}

		if provider == "github" && !strings.Contains(view, "Existing Google user? Choose Google first.") {
			t.Fatal("GitHub sign-in should direct existing Google users to their original account")
		}

		if provider == "google" && !strings.Contains(view, "Connect GitHub from your profile afterward.") {
			t.Fatal("Google sign-in should explain how to connect GitHub")
		}
	}
}

func TestProviderSelectionAndFailedLinkPreservesSession(t *testing.T) {
	m := model{width: 48, height: 16}
	if m.loginProvider() != "github" {
		t.Fatal("GitHub should be the initial login choice")
	}

	updated, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyTab})
	updated, _ = updated.Update(tea.KeyPressMsg{Code: tea.KeyTab})

	m = updated.(model)
	if m.loginProvider() != "google" {
		t.Fatal("Tab must allow Google login")
	}

	if !strings.Contains(ansi.Strip(m.authView().Content), "Sign in with Google") {
		t.Fatal("Google button missing")
	}

	updated, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})

	m = updated.(model)
	if cmd == nil || !m.loggingIn || m.loginProvider() != "google" {
		t.Fatal("Enter did not start the selected provider")
	}

	m.token, m.user, m.screen, m.form = "existing-session", api.User{
		ID:       "1",
		Username: "Owner",
	}, newPostScreen, newPostForm()
	m.form.fields[0].insert("Keep this draft")
	updated, _ = m.Update(githubLinked{authResult{err: errors.New("connection denied")}})

	m = updated.(model)
	if m.token != "existing-session" || m.form.fields[0].value != "Keep this draft" {
		t.Fatal("failed linking discarded the session or task")
	}
}

func TestTaskProfilesAndMenuRender(t *testing.T) {
	workerID := int64(2)

	post := api.Post{ID: 10, UserID: 1, AcceptedBy: &workerID,
		Poster: &api.PublicUser{ID: 1, GitHubLogin: "requester", GitHubURL: "https://github.com/requester"},
		Worker: &api.PublicUser{ID: 2, GitHubLogin: "worker", GitHubURL: "https://github.com/worker"}}
	for _, size := range [][2]int{{48, 16}, {80, 24}, {120, 36}} {
		m := model{
			width:  size[0],
			height: size[1],
			screen: detailScreen,
			token:  "session",
			posts:  []api.Post{post},
		}

		rows := strings.Join(m.detailRows(), "\n")
		if !strings.Contains(rows, "Posted by  @requester") ||
			!strings.Contains(rows, "Worker     @worker") ||
			!strings.Contains(rows, "https://github.com/requester") {
			t.Fatal("task participants need their public identities")
		}

		m.profileOpen = true
		for _, row := range strings.Split(m.View().Content, "\n") {
			if ansi.StringWidth(row) > size[0] {
				t.Fatal("GitHub profile menu overflows the terminal")
			}
		}
	}
}
