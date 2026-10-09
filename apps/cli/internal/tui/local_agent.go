package tui

import (
	"context"
	"errors"
	"strings"
	"sync"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/rajandhamala/Maruvo/cli/internal/providers"
)

type localAgentState struct {
	open, busy       bool
	autoSend         bool
	postsChanged     bool
	fundingTask      int64
	workspaceTask    int64
	input            textField
	client           *providers.Client
	marketplace      *providers.Marketplace
	label            string
	directory        string
	usage            string
	status           string
	history          []providers.Message
	lines            []string
	toolRows         map[string]int
	render           *localAgentRender
	files            agentFilePicker
	streamRow        int
	streaming        bool
	phase            string
	started          time.Time
	checkpoint       time.Time
	frame            int
	thinking         string
	thinkingAt       int
	thinkStarted     time.Time
	thinkTime        time.Duration
	thinkOpen        bool
	chat             providers.Conversation
	chatScope        providers.ChatScope
	storageErr       error
	sessions         localSessionsState
	provider         string
	model            string
	scroll           int
	sequence         uint64
	ctx              context.Context
	cancel           context.CancelFunc
	events           chan localAgentUpdate
	approval         *providers.Approval
	approvalGuidance *string
	approvalInput    textField
	approvalEditing  bool
	approvalChoice   int
	approvalScroll   int
	answer           chan bool
	err              error
}

type localAgentReady struct {
	sequence    uint64
	client      *providers.Client
	marketplace *providers.Marketplace
	label       string
	provider    string
	model       string
	err         error
}

type localAgentUpdate struct {
	sequence uint64
	event    providers.Event
	leading  []providers.Event
	approval *providers.Approval
	guidance *string
	answer   chan bool
	done     bool
	history  []providers.Message
	err      error
}

func (m model) sendDashboardPrompt() (tea.Model, tea.Cmd) {
	if m.loading || strings.TrimSpace(m.homeInput.value) == "" {
		return m, nil
	}

	next, cmd := m.openLocalAgent()
	m = next.(model)
	m.localAgent.input = textField{limit: 12000, byteLimit: 16000, multiline: true}
	m.localAgent.input.insert(m.homeInput.value)
	m.localAgent.autoSend = true

	return m, cmd
}

func (m model) openLocalAgent() (tea.Model, tea.Cmd) {
	m.commands.open = false

	previous := m.localAgent
	if previous.directory != m.localDirectory() {
		previous.history, previous.lines, previous.usage = nil, nil, ""
	}

	m.localAgent = localAgentState{
		open:      true,
		busy:      true,
		input:     textField{limit: 12000, byteLimit: 16000, multiline: true},
		sequence:  m.localAgent.sequence + 1,
		directory: m.localDirectory(),
		history:   previous.history,
		lines:     previous.lines,
		usage:     previous.usage,
		chat:      previous.chat,
		render:    &localAgentRender{},
		chatScope: providers.ChatScope{Profile: m.profile, Account: m.user.ID},
	}
	if m.client != nil {
		m.localAgent.chatScope.APIURL = m.client.URL()
	}

	if previous.directory != m.localDirectory() || previous.chatScope != m.localAgent.chatScope {
		m.localAgent.chat = providers.Conversation{}
		m.localAgent.history, m.localAgent.lines, m.localAgent.usage = nil, nil, ""
	}

	m.localAgent.input.insert(previous.input.value)
	profile, sequence, apiClient := m.profile, m.localAgent.sequence, m.client

	return m, func() tea.Msg {
		config, err := providers.LoadConfig(profile)
		if err != nil {
			return localAgentReady{sequence: sequence, err: err}
		}

		if config.Active == "" {
			return localAgentReady{
				sequence: sequence,
				err:      errors.New("Connect a model with /model, then send your prompt again."),
			}
		}

		client, err := providers.ConnectedClient(profile, config.Active)

		selected := providers.Model{ID: config.Connections[config.Active].Model}

		label := config.Active + " / " + selected.DisplayName()
		if reasoning := config.Connections[config.Active].Reasoning; reasoning != "" {
			label += " / reasoning " + reasoning
		}

		if err != nil {
			return localAgentReady{sequence: sequence, err: err}
		}

		marketplace, err := providers.LoadMarketplace(apiClient, profile)
		if err != nil {
			return localAgentReady{sequence: sequence, err: err}
		}

		if marketplace != nil {
			marketplace.SetAccount(m.user)
			marketplace.FundingUI = true
			marketplace.Commands = true
			label += " / Maruvo tools"
		}

		return localAgentReady{
			sequence:    sequence,
			client:      client,
			marketplace: marketplace,
			label:       label,
			provider:    config.Active,
			model:       selected.ID,
			err:         err,
		}
	}
}

func (m model) waitLocalAgent() tea.Cmd {
	events, ctx, sequence := m.localAgent.events, m.localAgent.ctx, m.localAgent.sequence

	return func() tea.Msg {
		select {
		case update := <-events:
			if update.event.Type != "assistant_delta" && update.event.Type != "reasoning_delta" {
				return update
			}

			var text strings.Builder
			text.WriteString(update.event.Text)

			timer := time.NewTimer(50 * time.Millisecond)
			defer timer.Stop()

			for {
				select {
				case next := <-events:
					if next.event.Type != update.event.Type || next.done || next.approval != nil {
						next.leading = []providers.Event{{Type: update.event.Type, Text: text.String()}}
						return next
					}

					text.WriteString(next.event.Text)
				case <-timer.C:
					update.event.Text = text.String()
					return update
				case <-ctx.Done():
					return localAgentUpdate{sequence: sequence, done: true, err: ctx.Err(),
						leading: []providers.Event{{Type: update.event.Type, Text: text.String()}}}
				}
			}
		case <-ctx.Done():
			return localAgentUpdate{sequence: sequence, done: true, err: ctx.Err()}
		}
	}
}

func (m model) sendLocalPrompt() (tea.Model, tea.Cmd) {
	a := &m.localAgent
	if !a.busy && errors.Is(a.err, providers.ErrToolBatchPaused) {
		if strings.TrimSpace(a.input.value) == "" {
			a.input.insert("Continue from the last verified tool result. Do not repeat completed mutations; inspect current task state before proceeding.")
		}
		m.workAgent.paused = false
	}
	if a.busy || a.client == nil || strings.TrimSpace(a.input.value) == "" {
		return m, nil
	}

	prompt, directory, history, client := a.input.value, m.localDirectory(), a.history, a.client
	if a.chat.ID == "" {
		var err error

		a.chat, err = providers.NewConversation(
			directory,
			a.client.SafeChatText(prompt, a.marketplace),
			a.provider,
			a.model,
		)
		if err != nil {
			a.storageErr = err
		}
	}

	a.chat.Pending = true
	a.chat.Messages = append(a.chat.Messages, providers.ChatMessage{Role: "user", Content: prompt})
	marketplace := a.marketplace
	fullAccess := m.hasFullAccess()
	if m.permissions.remembered == nil {
		m.permissions.remembered = &sync.Map{}
	}
	permissions := m
	if marketplace != nil {
		copy := *marketplace
		copy.FullAccess = fullAccess
		copy.PermissionMode = permissionModes[m.permissionMode()].name
		marketplace = &copy
	}
	a.sequence++
	a.busy, a.err, a.scroll, a.status = true, nil, 0, ""
	a.streaming, a.phase, a.started = false, "Waiting for model", time.Now()
	a.thinking, a.thinkStarted, a.thinkTime = "", time.Time{}, 0
	a.checkpoint, a.frame = time.Now(), 0
	a.lines = append(a.lines, "You: "+prompt, "")
	a.thinkingAt = len(a.lines)
	a.toolRows = make(map[string]int)
	a.input = textField{limit: 12000, byteLimit: 16000, multiline: true}
	a.files = agentFilePicker{sequence: a.files.sequence + 1}
	a.ctx, a.cancel = context.WithCancel(m.ctx)
	a.events = make(chan localAgentUpdate, 16)
	ctx, events, sequence := a.ctx, a.events, a.sequence
	work := m.workAgent
	worker := work.active && m.isWorker(m.workspace.Post)
	apiClient, token := m.client, m.token

	go func() {
		transportCtx := ctx
		send := func(update localAgentUpdate) bool {
			update.sequence = sequence
			select {
			case events <- update:
				return true
			case <-transportCtx.Done():
				return false
			}
		}
		approve := func(ctx context.Context, change providers.Approval) (bool, error) {
			if permissions.permitsAction(change.Action) || permissions.rememberedApproval(change) {
				if err := ctx.Err(); err != nil {
					return false, err
				}
				return true, nil
			}
			answer := make(chan bool, 1)
			guidance := ""
			if worker {
				_ = apiClient.ReportActivity(ctx, token, work.postID, work.runID, "waiting_for_answer", "Waiting for local action approval")
			}
			if !send(localAgentUpdate{approval: &change, answer: answer, guidance: &guidance}) {
				return false, ctx.Err()
			}

			select {
			case approved := <-answer:
				if worker {
					_ = apiClient.ReportActivity(ctx, token, work.postID, work.runID, "working", "Local action approval answered")
				}
				if !approved && guidance != "" {
					return false, errors.New("User declined this action. Follow this guidance instead: " + guidance)
				}
				return approved, nil
			case <-ctx.Done():
				return false, ctx.Err()
			}
		}
		finishActivity := func(bool, bool) {}
		if worker {
			if err := apiClient.ReportActivity(ctx, token, work.postID, work.runID, "working", "Built-in task agent executing"); err != nil {
				send(localAgentUpdate{done: true, err: err})
				return
			}
			workerCtx, stop := context.WithCancel(ctx)
			defer stop()
			ctx = workerCtx
			go func() {
				ticker := time.NewTicker(15 * time.Second)
				defer ticker.Stop()
				for {
					select {
					case <-workerCtx.Done():
						return
					case <-ticker.C:
						if err := apiClient.ReportActivity(workerCtx, token, work.postID, work.runID, "", ""); err != nil {
							stop()
							return
						}
					}
				}
			}()
			finishActivity = func(failed, paused bool) {
				interrupted := workerCtx.Err() != nil || failed
				stop()
				cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				state, detail := "waiting_for_answer", "Listening for task updates"
				if paused {
					state, detail = "waiting_for_answer", "Tool batch paused; waiting for worker to Continue"
				}
				if interrupted {
					state, detail = "interrupted", "Built-in task agent interrupted"
				}
				_ = apiClient.ReportActivity(cleanup, token, work.postID, work.runID, state, detail)
			}
		}
		updated, err := client.RunAgent(
			ctx,
			directory,
			history,
			prompt,
			approve,
			func(event providers.Event) {
				if worker && event.Type == "tool" && event.Status == "running" {
					detail := []rune(strings.SplitN(event.Text, " · ", 2)[0])
					_ = apiClient.ReportActivity(ctx, token, work.postID, work.runID, "working", string(detail[:min(1000, len(detail))]))
				}
				send(localAgentUpdate{event: event})
			},
			marketplace,
		)
		finishActivity(err != nil && !errors.Is(err, providers.ErrToolBatchPaused), errors.Is(err, providers.ErrToolBatchPaused))
		send(localAgentUpdate{done: true, history: updated, err: err})
	}()

	return m, tea.Batch(m.waitLocalAgent(), m.saveLocalConversation(), m.tickLocalAgent())
}

func (m model) updateLocalAgent(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	a := &m.localAgent
	if a.approval != nil {
		return m.updateActionApproval(msg)
	}
	if a.sessions.open {
		return m.updateLocalSessions(msg)
	}

	if !a.busy && a.files.open {
		switch msg.String() {
		case "up":
			a.files.selection = max(0, a.files.selection-1)
			return m, nil
		case "down":
			a.files.selection = min(max(0, len(a.files.items)-1), a.files.selection+1)
			return m, nil
		case "enter", "tab":
			return m.selectAgentFile()
		case "esc":
			a.files = agentFilePicker{sequence: a.files.sequence + 1}
			return m, nil
		}
	}

	switch msg.String() {
	case "esc":
		if m.workAgent.active {
			m.stopWorkAgent("Task agent stopped.")
		}
		if a.cancel != nil {
			a.cancel()
		}

		changed := a.postsChanged
		working := a.busy && a.client != nil
		a.finishStream(true)

		a.sequence++
		a.busy, a.autoSend, a.postsChanged = false, false, false

		a.approval, a.answer = nil, nil
		if working {
			if a.chat.ID != "" {
				a.history = providers.ConversationHistory(a.chat)
			}

			a.status = "Request stopped. Continue below."
		} else {
			a.open = false
		}

		if changed && m.token != "" && m.onDashboard() {
			m.loading = true
			m.dashboard.generation++

			return m, tea.Batch(m.fetchDashboard(), m.saveLocalConversation())
		}

		return m, m.saveLocalConversation()
	case "ctrl+r", "ctrl+h":
		if m.workAgent.active {
			m.notice = "Stop the task agent before switching chats."
			return m, nil
		}
		return m.openLocalSessions()
	case "pgup":
		a.scroll += max(1, m.height/2)
	case "pgdown":
		a.scroll = max(0, a.scroll-max(1, m.height/2))
	case "ctrl+n":
		if m.workAgent.active {
			m.notice = "Stop the task agent before starting a new chat."
			return m, nil
		}
		if !a.busy {
			return m.newLocalConversation()
		}
	case "ctrl+o":
		a.thinkOpen = !a.thinkOpen
		a.scroll = 0
	default:
		if !a.busy {
			if msg.String() == "enter" {
				return m.sendLocalPrompt()
			}

			if msg.String() == "shift+enter" {
				a.input.insert("\n")
				return m, m.completeAgentInput()
			}

			a.input.key(msg)

			return m, m.completeAgentInput()
		}
	}

	return m, nil
}

func (m model) localView(title string, rows []string, footer string) tea.View {
	width, height := m.dimensions()

	lines := []string{accent("  MARUVO") + "   " + muted(title), ""}
	for _, row := range rows[:min(len(rows), max(1, height-5))] {
		lines = append(lines, "  "+ansi.Truncate(row, max(12, width-4), "…"))
	}

	for len(lines) < height-2 {
		lines = append(lines, "")
	}

	if !m.shortcutsOpen {
		footer = compactHint(footer, max(12, width-4))
	}

	lines = append(lines, "  "+muted(ansi.Truncate(footer, max(12, width-4), "…")), "")
	for i, line := range lines {
		lines[i] = dashboardPanelRow(line, width)
	}

	view := tea.NewView(strings.Join(lines, "\n"))
	view.AltScreen = true

	return view
}

// Task agents keep their transcript in the workspace rather than switching chats.
func (m model) localAgentVisible() bool {
	return m.localAgent.open && !(m.workAgent.active && m.screen == workspaceScreen)
}
