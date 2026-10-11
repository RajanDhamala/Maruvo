package tui

import (
	tea "charm.land/bubbletea/v2"
	"github.com/rajandhamala/Maruvo/cli/internal/api"
)

type postChanged struct {
	post      api.Post
	created   bool
	wallet    string
	accepted  bool
	recovered string
	deletedID int64
	err       error
}

func (m model) createPost(payload api.CreatePostPayload) tea.Cmd {
	return func() tea.Msg {
		key, err := m.loadWallet(m.ctx)
		if err != nil {
			return postChanged{err: err}
		}

		post, err := m.client.CreatePost(m.ctx, m.token, payload)

		return postChanged{post: post, created: true, wallet: key.Address(), err: err}
	}
}

func (m model) updateStatus(post api.Post) tea.Cmd {
	return func() tea.Msg {
		updated, err := m.client.UpdateStatus(m.ctx, m.token, post.ID, statuses[m.statusChoice])
		return postChanged{post: updated, err: err}
	}
}

func (m model) deletePost(post api.Post) tea.Cmd {
	return func() tea.Msg {
		err := m.client.DeletePost(m.ctx, m.token, post.ID)
		return postChanged{deletedID: post.ID, err: err}
	}
}

func (m model) recoverPost(post api.Post) tea.Cmd {
	return func() tea.Msg {
		updated, err := m.client.RecoverPost(
			m.ctx,
			m.token,
			post.ID,
			m.recovering,
			m.recoveryDeadline,
			m.recoveryDelivery,
		)

		return postChanged{post: updated, recovered: m.recovering, err: err}
	}
}
