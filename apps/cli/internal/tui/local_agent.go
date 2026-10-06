package tui

import (
	"context"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/rajandhamala/Maruvo/cli/internal/providers"
)

type localAgentState struct {
	open, busy bool
	input      textField
	client     *providers.Client
	label      string
	history    []providers.Message
	lines      []string
	scroll     int
	sequence   uint64
	ctx        context.Context
	cancel     context.CancelFunc
	events     chan localAgentUpdate
	approval   *providers.Approval
	answer     chan bool
	err        error
}

type localAgentReady struct {
	sequence uint64
	client   *providers.Client
	label    string
	err      error
}

type localAgentUpdate struct {
	sequence uint64
	event    providers.Event
	approval *providers.Approval
	answer   chan bool
	done     bool
	history  []providers.Message
	err      error
}

func (m model) openLocalAgent() (tea.Model, tea.Cmd) {
	m.commands.open = false
	m.localAgent = localAgentState{
		open: true, busy: true, input: textField{limit: 12000, byteLimit: 16000, multiline: true},
		sequence: m.localAgent.sequence + 1,
	}
	profile, sequence := m.profile, m.localAgent.sequence

	return m, func() tea.Msg {
		config, err := providers.LoadConfig(profile)
		if err != nil {
			return localAgentReady{sequence: sequence, err: err}
		}

		client, err := providers.ConnectedClient(profile, config.Active)
		label := config.Active + " / " + config.Connections[config.Active].Model

		return localAgentReady{sequence: sequence, client: client, label: label, err: err}
	}
}

func (m model) waitLocalAgent() tea.Cmd {
	events, ctx, sequence := m.localAgent.events, m.localAgent.ctx, m.localAgent.sequence

	return func() tea.Msg {
		select {
		case update := <-events:
			return update
		case <-ctx.Done():
			return localAgentUpdate{sequence: sequence, done: true, err: ctx.Err()}
		}
	}
}

func (m model) sendLocalPrompt() (tea.Model, tea.Cmd) {
	a := &m.localAgent
	if a.busy || a.client == nil || strings.TrimSpace(a.input.value) == "" {
		return m, nil
	}

	prompt, directory, history, client := a.input.value, m.localDirectory(), a.history, a.client
	a.sequence++
	a.busy, a.err, a.scroll = true, nil, 0
	a.lines = append(a.lines, "You: "+prompt, "")
	a.input = textField{limit: 12000, byteLimit: 16000, multiline: true}
	a.ctx, a.cancel = context.WithTimeout(m.ctx, 5*time.Minute)
	a.events = make(chan localAgentUpdate, 16)
	ctx, events, sequence := a.ctx, a.events, a.sequence

	go func() {
		send := func(update localAgentUpdate) bool {
			update.sequence = sequence
			select {
			case events <- update:
				return true
			case <-ctx.Done():
				return false
			}
		}
		approve := func(ctx context.Context, change providers.Approval) (bool, error) {
			answer := make(chan bool, 1)
			if !send(localAgentUpdate{approval: &change, answer: answer}) {
				return false, ctx.Err()
			}

			select {
			case approved := <-answer:
				return approved, nil
			case <-ctx.Done():
				return false, ctx.Err()
			}
		}
		updated, err := client.RunAgent(
			ctx,
			directory,
			history,
			prompt,
			approve,
			func(event providers.Event) {
				send(localAgentUpdate{event: event})
			},
		)
		send(localAgentUpdate{done: true, history: updated, err: err})
	}()

	return m, m.waitLocalAgent()
}

func (m model) updateLocalAgent(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	a := &m.localAgent

	switch msg.String() {
	case "esc":
		if a.cancel != nil {
			a.cancel()
		}

		m.localAgent = localAgentState{sequence: a.sequence + 1}

		return m, nil
	case "pgup":
		a.scroll += max(1, m.height/2)
	case "pgdown":
		a.scroll = max(0, a.scroll-max(1, m.height/2))
	case "ctrl+n":
		if !a.busy {
			a.history, a.lines, a.err, a.scroll = nil, nil, nil, 0
		}
	default:
		if a.approval != nil {
			if msg.String() == "y" || msg.String() == "n" {
				a.answer <- msg.String() == "y"

				a.approval, a.answer, a.scroll = nil, nil, 0
			}

			return m, nil
		}

		if !a.busy {
			if msg.String() == "enter" {
				return m.sendLocalPrompt()
			}

			a.input.key(msg)
		}
	}

	return m, nil
}

func (m model) localAgentView() tea.View {
	a := m.localAgent
	width, height := m.dimensions()
	rows := []string{muted(a.label), muted("Folder: " + m.localDirectory()), ""}

	var transcript []string

	for _, line := range a.lines {
		transcript = append(
			transcript,
			strings.Split(ansi.Wrap(ansi.Strip(line), max(12, width-4), ""), "\n")...)
	}

	if a.approval != nil {
		transcript = append(transcript, "Proposed file: "+a.approval.Path)
		transcript = append(
			transcript,
			strings.Split(ansi.Wrap(ansi.Strip(a.approval.Content), max(12, width-4), ""), "\n")...)
	}

	visible := max(1, height-13)
	end := max(0, len(transcript)-min(a.scroll, max(0, len(transcript)-visible)))
	start := max(0, end-visible)

	rows = append(rows, transcript[start:end]...)
	for len(rows) < visible+3 {
		rows = append(rows, "")
	}

	if a.err != nil {
		rows = append(rows, warning(plain(a.err.Error())))
	} else if a.approval != nil {
		rows = append(rows, accent("Apply "+plain(a.approval.Path)+"? y / n"))
	} else if a.busy {
		rows = append(rows, accent("Agent working... Esc stops the request."))
	} else {
		rows = append(rows, muted("Read files freely; edits ask for approval."))
	}

	rows = append(rows, "", "› "+ansi.Truncate(plain(a.input.value), max(12, width-6), "…"))

	return m.localView("CLI agent", rows, "Enter send · PgUp/PgDn history · Ctrl+n new · Esc back")
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

	lines = append(lines, "  "+muted(ansi.Truncate(footer, max(12, width-4), "…")), "")
	for i, line := range lines {
		lines[i] = dashboardPanelRow(line, width)
	}

	view := tea.NewView(strings.Join(lines, "\n"))
	view.AltScreen = true

	return view
}
