package tui

import (
	tea "charm.land/bubbletea/v2"
	"github.com/rajandhamala/Maruvo/cli/internal/api"
)

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
	case tea.MouseClickMsg:
		return m.updateMouse(msg)
	case tea.MouseWheelMsg:
		return m.updateWheel(msg)
	case tea.PasteMsg:
		if m.token != "" && m.screen == workspaceScreen && m.workspaceAction != "" && !m.loading &&
			!m.profileOpen {
			m.workspaceInput.insert(msg.Content)
		} else if m.token != "" && m.composing() && !m.loading &&
			!m.profileOpen {
			m.composer.paste(msg.Content)
			return m, m.completeFiles()
		} else if m.token != "" && m.picker.open &&
			!m.loading {
			m.picker.insert(msg.Content)
		} else if m.token != "" && m.screen == newPostScreen && !m.loading && !m.profileOpen && m.form.focus < len(m.form.fields) &&
			m.form.focus != 2 {
			m.form.fields[m.form.focus].insert(msg.Content)
		}
	case tea.KeyPressMsg:
		if msg.String() == "ctrl+c" {
			m.stopWorkspace()
			return m, tea.Quit
		}

		if m.token != "" && !m.demo {
			if m.picker.open {
				return m.updateDeadline(msg)
			}

			if !m.loading && msg.String() == "ctrl+p" {
				m.profileOpen = !m.profileOpen
				return m, nil
			}

			if m.profileOpen {
				return m.updateProfile(msg)
			}

			return m.updatePosts(msg)
		}

		switch msg.String() {
		case "q", "ctrl+c", "esc":
			return m, tea.Quit
		case "enter", "r":
			if !m.loading {
				m.loading, m.err = true, nil
				if m.demo {
					return m, m.sendDemo()
				}

				if msg.String() == "enter" && m.token == "" {
					m.loggingIn = true
					return m, m.signIn()
				}

				return m, m.restoreSession()
			}
		case "l":
			if !m.demo && !m.loading {
				m.loading, m.err = true, nil
				if m.token != "" {
					return m, m.logout()
				}

				m.loggingIn = true

				return m, m.signIn()
			}
		}
	case demoResult:
		m.loading = false
		m.response, m.err = msg.response, msg.err
	case workspaceLoaded:
		return m.workspaceLoaded(msg)
	case localFilesFound:
		if m.screen == workspaceScreen && msg.generation == m.workspaceGen &&
			msg.sequence == m.composer.sequence &&
			m.composer.completing {
			m.composer.suggestions = msg.files
			if msg.err != nil {
				m.composer.fileError = msg.err.Error()
			}
		}
	case chatSent:
		return m.chatSent(msg)
	case reviewPrepared:
		return m.reviewPrepared(msg)
	case workspaceConnected:
		return m.workspaceConnected(msg)
	case workspaceFrame:
		return m.workspaceFrame(msg)
	case workspaceRetry:
		if m.screen == workspaceScreen && msg.generation == m.workspaceGen && m.workspaceCancel != nil {
			m.live = "Connecting..."
			return m, m.connectWorkspace()
		}
	case workspaceActionResult:
		if msg.generation != m.workspaceGen || m.screen != workspaceScreen {
			return m, nil
		}

		m.loading = false
		m.setPostError(msg.err)

		if msg.err == nil {
			m.notice = msg.notice
			m.workspaceAction, m.workspaceInput, m.reviewConfirm = "", textField{}, false
		}
	case walletResult:
		m.loading = false
		m.setPostError(msg.err)

		if msg.err == nil {
			m.walletAddress = msg.address
			m.notice = "Wallet connected. Payments need your separate approval."
		}
	case escrowResult:
		m.loading = false
		m.setPostError(msg.err)

		if msg.err != nil {
			return m, nil
		}

		for i := range m.posts {
			if m.posts[i].ID == msg.info.Post.ID {
				m.posts[i] = msg.info.Post
			}
		}

		m.escrow, m.fundingConfirm = msg.info.Escrow, msg.confirm
		if msg.submitted {
			m.notice = "Funding submitted. Refresh to verify confirmation."
		}

		if msg.info.Escrow.State == "confirmed" {
			m.notice = "Escrow funded. Work can begin."
		}
	case authResult:
		m.picker.open = false
		m.profileOpen = false

		m.loading, m.loggingIn, m.err = false, false, msg.err
		if msg.err == nil {
			m.user, m.token = msg.user, msg.token

			m.walletAddress = msg.wallet
			if m.token != "" {
				return m.openPosts(false)
			}
		} else {
			m.user, m.token = api.User{}, ""
		}
	case logoutResult:
		m.picker.open = false
		m.profileOpen = false

		m.loading, m.err = false, msg.err
		if msg.err == nil {
			m.user, m.token = api.User{}, ""
			m.walletAddress = ""
			m.posts, m.notice = nil, ""
		}
	case postsResult:
		m.loading = false
		m.setPostError(msg.err)

		if msg.err == nil {
			m.posts = msg.posts

			m.selected = min(m.selected, max(0, len(m.posts)-1))
			if m.screen == detailScreen && len(m.posts) == 0 {
				m.screen = feedScreen
				if m.own {
					m.screen = myPostsScreen
				}
			}
		}
	case postChanged:
		m.loading = false
		m.setPostError(msg.err)

		if msg.err != nil {
			return m, nil
		}

		if msg.wallet != "" {
			m.walletAddress = msg.wallet
		}

		m.deleting, m.editingStatus = false, false

		switch {
		case msg.accepted:
			m.walletAddress = msg.post.WorkerWallet
			m.posts[m.selected] = msg.post
			m.notice = "Accepted. Wait for the poster to fund escrow before starting work."
			m.escrow = api.Escrow{State: "unfunded"}

			return m.openWorkspace("")
		case msg.created:
			m.screen, m.own, m.selected = myPostsScreen, true, 0
			m.form, m.posts = postForm{}, nil
			m.notice, m.loading = "Post created.", true

			return m, m.fetchPosts()
		case msg.deletedID != 0:
			m.screen, m.notice, m.loading = myPostsScreen, "Post deleted.", true
			return m, m.fetchPosts()
		default:
			for i := range m.posts {
				if m.posts[i].ID == msg.post.ID {
					m.posts[i] = msg.post
				}
			}

			m.notice = "Status updated."
		}
	}

	return m, nil
}
