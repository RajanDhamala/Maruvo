package tui

import (
	tea "charm.land/bubbletea/v2"
	"github.com/rajandhamala/Maruvo/cli/internal/api"
	"github.com/rajandhamala/Maruvo/cli/internal/wallet"
)

type postChanged struct {
	post      api.Post
	created   bool
	wallet    string
	accepted  bool
	deletedID int64
	err       error
}

func (m model) createPost(payload api.CreatePostPayload) tea.Cmd {
	return func() tea.Msg {
		key, err := wallet.Load()
		if err != nil {
			return postChanged{err: err}
		}

		if err = m.linkWallet(key); err != nil {
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
