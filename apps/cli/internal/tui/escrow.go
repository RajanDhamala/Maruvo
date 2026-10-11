package tui

import (
	"context"
	"errors"
	"os"
	"strconv"

	tea "charm.land/bubbletea/v2"
	"github.com/rajandhamala/Maruvo/cli/internal/api"
	"github.com/rajandhamala/Maruvo/cli/internal/auth"
	"github.com/rajandhamala/Maruvo/cli/internal/wallet"
)

type walletResult struct {
	address string
	err     error
	browser bool
}
type escrowResult struct {
	info               api.PostInfo
	confirm, submitted bool
	err                error
}

type agentFundingLoaded struct {
	info api.PostInfo
	err  error
}

func (m model) isPoster(post api.Post) bool { return strconv.FormatInt(post.UserID, 10) == m.user.ID }

func (m model) isWorker(post api.Post) bool {
	return post.AcceptedBy != nil && strconv.FormatInt(*post.AcceptedBy, 10) == m.user.ID
}

func (m model) connectWallet() tea.Cmd {
	return func() tea.Msg {
		if os.Getenv("MARUVO_WALLET") == "browser" {
			address, err := wallet.ConnectBrowser(m.ctx, m.client, m.token)
			return walletResult{address: address, err: err}
		}
		key, err := wallet.Load()
		if err != nil {
			return walletResult{err: err}
		}

		err = m.linkWallet(key)

		return walletResult{address: key.Address(), err: err}
	}
}

func (m model) connectBrowserWallet() tea.Cmd {
	return func() tea.Msg {
		address, err := wallet.ConnectBrowser(m.ctx, m.client, m.token)
		if err == nil {
			err = auth.SaveWallet(m.profile, "browser")
		}
		return walletResult{address: address, err: err, browser: true}
	}
}

func (m model) loadWallet(ctx context.Context) (*wallet.Wallet, error) {
	if os.Getenv("MARUVO_WALLET") != "browser" {
		key, err := wallet.Load()
		if err != nil {
			return nil, err
		}
		if err := m.linkWallet(key); err != nil {
			return nil, err
		}
		return key, nil
	}
	address, err := m.client.Wallet(ctx, m.token)
	if err != nil {
		return nil, err
	}
	if address == "" {
		address, err = wallet.ConnectBrowser(ctx, m.client, m.token)
		if err != nil {
			return nil, err
		}
	}
	return wallet.Browser(ctx, address)
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
		_, err := m.loadWallet(m.ctx)
		if err != nil {
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
		_, err := m.loadWallet(m.ctx)
		if err != nil {
			return escrowResult{err: err}
		}

		info, err := m.client.PrepareFunding(m.ctx, m.token, post.ID)

		return escrowResult{info: info, confirm: err == nil && info.Escrow.State == "prepared", err: err}
	}
}

func (m model) submitFunding() tea.Cmd {
	post, plan := m.posts[m.selected], m.escrow

	return func() tea.Msg {
		key, err := m.loadWallet(m.ctx)
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
