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
		if m.token == "" && !m.demo && !m.loading {
			m.homeFocus, m.homeInput.limit = homePrompt, 2000
			m.homeInput.insert(msg.Content)
		} else if m.token != "" && m.onDashboard() && !m.demo && !m.loading && !m.profileOpen &&
			!m.dashboard.filterOpen {
			if m.dashboard.focus == dashboardSearch {
				m.dashboard.search.limit = 200
				m.dashboard.search.insert(msg.Content)
				m.selectVisiblePost()
			} else {
				m.dashboard.promptHidden = false
				m.dashboard.focus, m.homeInput.limit = dashboardPrompt, 2000
				m.homeInput.insert(msg.Content)
			}
		} else if m.token != "" && m.screen == workspaceScreen && m.workspaceAction != "" && !m.loading &&
			!m.profileOpen {
			m.workspaceInput.insert(msg.Content)
		} else if m.token != "" && m.composing() && !m.loading &&
			!m.profileOpen {
			m.composer.paste(msg.Content)
			return m, m.completeFiles()
		} else if m.token != "" && m.picker.open &&
			!m.loading {
			m.picker.insert(msg.Content)
		} else if m.token != "" && m.screen == newPostScreen && m.form.importing && !m.loading && !m.profileOpen {
			m.form.path.insert(msg.Content)
			return m, m.searchDescriptionFiles()
		} else if m.token != "" && m.screen == newPostScreen && !m.loading && !m.profileOpen && m.form.focus < len(m.form.fields) &&
			m.form.focus != 2 {
			if m.form.focus == 3 && m.form.descriptionSource == descriptionFromFile {
				return m, nil
			}

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
				m.dashboard.filterOpen = false
				m.profileOpen = !m.profileOpen

				return m, nil
			}

			if !m.loading && msg.String() == "ctrl+g" {
				return m.beginGitHubLink()
			}

			if m.profileOpen {
				return m.updateProfile(msg)
			}

			return m.updatePosts(msg)
		}

		if !m.demo {
			return m.updateAuth(msg)
		}

		switch msg.String() {
		case "q", "esc":
			return m, tea.Quit
		case "enter", "r":
			if !m.loading {
				m.loading, m.err = true, nil
				return m, m.sendDemo()
			}
		}
	case demoResult:
		m.loading = false
		m.response, m.err = msg.response, msg.err
	case descriptionLoaded:
		return m.descriptionLoaded(msg)
	case descriptionFilesFound:
		if m.screen == newPostScreen && m.form.importing && msg.sequence == m.descriptionSearchSeq {
			m.form.fileSearch.suggestions, m.form.fileSearch.err = msg.files, msg.err
			m.form.fileSearch.searching = false
		}
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

		m.updateDashboardPost(msg.info.Post)

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
			m.clearDashboard()
			m.dashboard.all = true

			m.walletAddress = msg.wallet
			if m.token != "" {
				return m.openPosts(false)
			}
		} else {
			m.user, m.token = api.User{}, ""
		}
	case githubLinked:
		m.loading, m.loggingIn, m.err = false, false, msg.err
		if msg.err == nil {
			m.user, m.token = msg.user, msg.token
			m.notice = "GitHub connected. Refresh tasks to update their profiles."
		}
	case logoutResult:
		m.picker.open = false
		m.profileOpen = false

		m.loading, m.err = false, msg.err
		if msg.err == nil {
			m.user, m.token = api.User{}, ""
			m.walletAddress = ""
			m.posts, m.notice = nil, ""
			m.clearDashboard()
		}
	case dashboardResult:
		if msg.generation != m.dashboard.generation || m.token == "" || !m.onDashboard() {
			return m, nil
		}

		m.loading = false
		m.setPostError(msg.err)

		if msg.err == nil {
			m.dashboard.feed, m.dashboard.mine, m.dashboard.ready = msg.feed, msg.mine, true
			m.applyDashboardPosts()
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
			m.dashboard.ready = false
			m.walletAddress = msg.post.WorkerWallet
			m.posts[m.selected] = msg.post
			m.notice = "Accepted. Wait for the poster to fund escrow before starting work."
			m.escrow = api.Escrow{State: "unfunded"}

			return m.openWorkspace("")
		case msg.created:
			m.homeInput = textField{}
			m.dashboard.search, m.dashboard.focus = textField{}, dashboardTasks
			m.dashboard.generation++
			m.screen, m.own, m.selected = myPostsScreen, true, 0
			m.taskFilter = 1
			m.form, m.posts = postForm{}, nil
			m.notice, m.loading = "Post created.", true

			return m, m.fetchDashboard()
		case msg.deletedID != 0:
			m.dashboard.generation++
			m.screen, m.own, m.notice, m.loading = myPostsScreen, true, "Post deleted.", true

			return m, m.fetchDashboard()
		default:
			m.updateDashboardPost(msg.post)

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
