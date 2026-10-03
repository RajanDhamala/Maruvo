package tui

import (
	tea "charm.land/bubbletea/v2"
	"github.com/rajandhamala/Maruvo/cli/internal/api"
	"github.com/rajandhamala/Maruvo/cli/internal/auth"
)

type demoResult struct {
	response api.DemoResponse
	err      error
}

type authResult struct {
	user   api.User
	token  string
	wallet string
	err    error
}

type logoutResult struct {
	err error
}

type githubLinked struct{ authResult }

func (m model) restoreSession() tea.Cmd {
	return func() tea.Msg {
		token, err := auth.LoadSession(m.client.URL(), m.profile)
		if err != nil || token == "" {
			return authResult{err: err}
		}

		user, err := m.client.Me(m.ctx, token)
		address, _ := m.client.Wallet(m.ctx, token)

		return authResult{user: user, token: token, wallet: address, err: err}
	}
}

func (m model) signIn() tea.Cmd {
	return func() tea.Msg {
		token, err := auth.LoginWithProvider(m.ctx, m.client, m.loginProvider())
		if err != nil {
			return authResult{err: err}
		}

		user, err := m.client.Me(m.ctx, token)
		if err == nil {
			err = auth.SaveSession(m.client.URL(), token, m.profile)
		}

		address, _ := m.client.Wallet(m.ctx, token)

		return authResult{user: user, token: token, wallet: address, err: err}
	}
}

func (m model) logout() tea.Cmd {
	return func() tea.Msg {
		return logoutResult{err: auth.ClearSession(m.client.URL(), m.profile)}
	}
}

func (m model) linkGitHub() tea.Cmd {
	return func() tea.Msg {
		token, err := auth.LinkGitHub(m.ctx, m.client, m.token)
		if err != nil {
			return githubLinked{authResult{err: err}}
		}

		user, err := m.client.Me(m.ctx, token)
		if err == nil {
			err = auth.SaveSession(m.client.URL(), token, m.profile)
		}

		return githubLinked{authResult{user: user, token: token, err: err}}
	}
}

func (m model) loginProvider() string {
	if m.authProvider == "google" {
		return "google"
	}

	return "github"
}

func (m model) beginSignIn(provider string) (tea.Model, tea.Cmd) {
	m.authProvider, m.loggingIn, m.loading, m.err = provider, true, true, nil
	return m, m.signIn()
}

func (m model) sendDemo() tea.Cmd {
	return func() tea.Msg {
		response, err := m.client.Demo(m.ctx, m.message)
		return demoResult{response: response, err: err}
	}
}
