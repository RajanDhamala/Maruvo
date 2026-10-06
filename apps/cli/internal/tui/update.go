package tui

import (
	tea "charm.land/bubbletea/v2"
	"github.com/rajandhamala/Maruvo/cli/internal/api"
	"github.com/rajandhamala/Maruvo/cli/internal/providers"
)

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case deadlineClock:
		return m, deadlineTick()
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
	case tea.MouseClickMsg:
		if m.providers.open {
			return m.updateProviderMouse(msg)
		}

		if m.localAgent.open {
			return m, nil
		}

		return m.updateMouse(msg)
	case tea.MouseWheelMsg:
		if m.providers.open {
			area := m.providerArea()
			if msg.X < area.x || msg.X >= area.x+area.width || msg.Y < area.y || msg.Y >= area.y+area.height {
				return m, nil
			}

			if msg.Button == tea.MouseWheelUp {
				return m.updateProviders(tea.KeyPressMsg{Code: tea.KeyUp})
			}

			if msg.Button == tea.MouseWheelDown {
				return m.updateProviders(tea.KeyPressMsg{Code: tea.KeyDown})
			}

			return m, nil
		}

		if m.localAgent.open {
			return m, nil
		}

		return m.updateWheel(msg)
	case tea.PasteMsg:
		if m.providers.open {
			if !m.providers.busy && m.providers.step == providerKey {
				m.providers.key.insert(msg.Content)
			} else if !m.providers.busy {
				m.providers.query.insert(msg.Content)
				m.providers.selection = 0
			}

			return m, nil
		}

		if m.localAgent.open {
			if !m.localAgent.busy {
				m.localAgent.input.insert(msg.Content)
			}

			return m, nil
		}

		if m.commands.open {
			m.commands.query.insert(msg.Content)
			return m.updateCommandQuery()
		}

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
		} else if m.token != "" && m.screen == newPostScreen && m.form.timingOpen && !m.loading && !m.profileOpen {
			if m.form.timingFocus == 0 || m.form.timingFocus == 2 {
				m.form.timings[m.form.timingFocus].insert(msg.Content)
			}
		} else if m.token != "" && m.screen == newPostScreen && !m.loading && !m.profileOpen && m.form.focus < len(m.form.fields) &&
			m.form.focus != 2 {
			if m.form.focus == 3 && m.form.descriptionSource == descriptionFromFile {
				return m, nil
			}

			m.form.fields[m.form.focus].insert(msg.Content)
		}
	case tea.KeyPressMsg:
		if msg.String() == "ctrl+c" {
			if m.localAgent.cancel != nil {
				m.localAgent.cancel()
			}

			if m.providers.cancel != nil {
				m.providers.cancel()
			}

			m.stopWorkspace()

			return m, tea.Quit
		}

		if m.providers.open {
			return m.updateProviders(msg)
		}

		if m.localAgent.open {
			return m.updateLocalAgent(msg)
		}

		if m.commands.open {
			return m.updateCommands(msg)
		}

		if msg.String() == "/" && m.canOpenCommands() {
			return m.openCommands(), nil
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
	case directoriesFound:
		if m.commands.open && m.commands.directory && msg.sequence == m.commands.sequence {
			m.commands.folders, m.commands.err, m.commands.searching = msg.folders, msg.err, false
		}
	case providerConfigLoaded:
		if m.providers.open && msg.sequence == m.providers.sequence {
			m.providers.busy, m.providers.config, m.providers.err = false, msg.config, msg.err
			for i, name := range providers.Names {
				if name == msg.config.Active {
					m.providers.provider, m.providers.selection = i, i
				}
			}
		}
	case providerModelsLoaded:
		if m.providers.open && msg.sequence == m.providers.sequence {
			m.providers.busy, m.providers.err = false, msg.err

			m.providers.key = textField{limit: 4096}

			m.providers.models, m.providers.secret = nil, ""
			if msg.err == nil {
				m.providers.models, m.providers.secret = msg.models, msg.secret
				m.providers.step, m.providers.selection = providerModel, 0
				m.providers.query = textField{limit: 200}

				name := providers.Names[m.providers.provider]
				for i, item := range msg.models {
					if item.ID == m.providers.config.Connections[name].Model {
						m.providers.selection = i
					}
				}
			}
		}
	case providerSaved:
		if m.providers.open && msg.sequence == m.providers.sequence {
			m.providers.busy, m.providers.saving, m.providers.err = false, false, msg.err

			m.providers.secret, m.providers.models = "", nil
			if msg.err != nil && !msg.removed {
				m.providers.step = providerKey
			}

			if msg.err == nil {
				m.providers = providerSettings{sequence: m.providers.sequence + 1}

				m.notice = "Provider connected. Open /agent to use the CLI agent."
				if msg.removed {
					m.notice = "Provider disconnected; its saved credential was removed."
				}
			}
		}
	case localAgentReady:
		if m.localAgent.open && msg.sequence == m.localAgent.sequence {
			m.localAgent.busy, m.localAgent.client, m.localAgent.label, m.localAgent.err = false, msg.client, msg.label, msg.err
		}
	case localAgentUpdate:
		if !m.localAgent.open || msg.sequence != m.localAgent.sequence {
			return m, nil
		}

		if msg.done {
			m.localAgent.busy, m.localAgent.err = false, msg.err

			m.localAgent.approval, m.localAgent.answer = nil, nil
			if msg.err == nil {
				m.localAgent.history = msg.history
			}

			if m.localAgent.cancel != nil {
				m.localAgent.cancel()
			}

			return m, nil
		}

		if msg.approval != nil {
			m.localAgent.approval, m.localAgent.answer, m.localAgent.scroll = msg.approval, msg.answer, 0
		} else if msg.event.Type == "assistant" {
			m.localAgent.lines = append(m.localAgent.lines, "Agent: "+msg.event.Text, "")
		} else {
			m.localAgent.lines = append(m.localAgent.lines, "Tool: "+msg.event.Text)
		}

		return m, m.waitLocalAgent()
	case directoryOpened:
		if !m.commands.open || !m.commands.directory || msg.sequence != m.commands.sequence {
			return m, nil
		}

		m.commands.err, m.commands.searching = msg.err, false
		if msg.err == nil {
			m.directory, m.homePath = msg.path, displayHomePath(msg.path)
			m.commands.open = false
			m.composer.root = msg.path
			m.composer.sequence++
			m.composer.suggestions, m.composer.completing = nil, false
			m.err, m.notice = nil, "Directory opened."
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
		case msg.recovered != "":
			m.recovering = ""
			m.dashboard.ready = false
			m.dashboard.generation++
			m.posts[m.selected] = msg.post

			m.notice = "Task canceled before funding. Its private history is retained."
			if m.escrow.Address != "" {
				m.escrow.State = "expired"
			}

			if msg.recovered == "reopen" {
				m.escrow = api.Escrow{State: "unfunded"}
				m.notice = "Task reopened under a new ID. Share supporting files with the new worker."
				m.loading = true

				return m, m.postInfo()
			}
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
