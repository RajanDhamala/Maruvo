package tui

import (
	"errors"
	"strconv"

	tea "charm.land/bubbletea/v2"
	"github.com/rajandhamala/Maruvo/cli/internal/api"
	"github.com/rajandhamala/Maruvo/cli/internal/wallet"
)

type walletResult struct {
	address string
	err     error
}
type escrowResult struct {
	info               api.PostInfo
	confirm, submitted bool
	err                error
}

func (m model) isPoster(post api.Post) bool { return strconv.FormatInt(post.UserID, 10) == m.user.ID }

func (m model) isWorker(post api.Post) bool {
	return post.AcceptedBy != nil && strconv.FormatInt(*post.AcceptedBy, 10) == m.user.ID
}

func (m model) connectWallet() tea.Cmd {
	return func() tea.Msg {
		key, err := wallet.Load()
		if err != nil {
			return walletResult{err: err}
		}

		err = m.linkWallet(key)

		return walletResult{address: key.Address(), err: err}
	}
}

func (m model) linkWallet(key *wallet.Wallet) error {
	address, err := m.client.Wallet(m.ctx, m.token)
	if err != nil {
		return err
	}

	if address == key.Address() {
		return nil
	}

	if address != "" {
		return errors.New("this account is linked to another wallet; use its keypair file")
	}

	message, err := m.client.WalletChallenge(m.ctx, m.token, key.Address())
	if err != nil {
		return err
	}

	return m.client.LinkWallet(m.ctx, m.token, key.SignMessage(message))
}

func (m model) acceptPost(post api.Post) tea.Cmd {
	return func() tea.Msg {
		key, err := wallet.Load()
		if err != nil {
			return postChanged{err: err}
		}

		if err = m.linkWallet(key); err != nil {
			return postChanged{err: err}
		}

		updated, err := m.client.AcceptPost(m.ctx, m.token, post.ID)

		return postChanged{post: updated, accepted: true, err: err}
	}
}

func (m model) postInfo() tea.Cmd {
	post := m.posts[m.selected]

	return func() tea.Msg {
		info, err := m.client.PostInfo(m.ctx, m.token, post.ID)
		return escrowResult{info: info, err: err}
	}
}

func (m model) prepareFunding() tea.Cmd {
	post := m.posts[m.selected]

	return func() tea.Msg {
		key, err := wallet.Load()
		if err != nil {
			return escrowResult{err: err}
		}

		if err = m.linkWallet(key); err != nil {
			return escrowResult{err: err}
		}

		info, err := m.client.PrepareFunding(m.ctx, m.token, post.ID)

		return escrowResult{info: info, confirm: err == nil && info.Escrow.State == "prepared", err: err}
	}
}

func (m model) submitFunding() tea.Cmd {
	post, plan := m.posts[m.selected], m.escrow

	return func() tea.Msg {
		key, err := wallet.Load()
		if err != nil {
			return escrowResult{err: err}
		}

		transaction, err := key.SignFunding(post, plan)
		if err != nil {
			return escrowResult{err: err}
		}

		info, err := m.client.SubmitFunding(m.ctx, m.token, post.ID, transaction)

		return escrowResult{info: info, submitted: true, err: err}
	}
}

func (m model) openDetail() (tea.Model, tea.Cmd) {
	m.stopWorkspace()
	m.screen, m.scroll, m.loading = detailScreen, 0, true
	m.err, m.notice, m.fundingConfirm = nil, "", false
	m.escrow = api.Escrow{}

	return m, m.postInfo()
}
