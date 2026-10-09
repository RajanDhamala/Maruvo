package tui

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/rajandhamala/Maruvo/cli/internal/api"
	"github.com/rajandhamala/Maruvo/cli/internal/providers"
)

type workSetup struct {
	autoReady, invite                  bool
	target                             string
	peerStatus, peerDetail             string
	open, busy, armed, peer, prepared  bool
	generation                         uint64
	postID                             int64
	nonce, pair, instruction, provider string
	err                                error
}
type workSetupTick struct{ generation uint64 }
type workSetupResult struct {
	generation uint64
	readiness  api.AgentReadiness
	provider   string
	err        error
}

func (m model) openWorkSetup(instruction string) (tea.Model, tea.Cmd) {
	if m.localAgent.busy {
		m.notice = "Finish or stop the current agent turn before configuring work."
		return m, nil
	}
	if m.workAgent.active && instruction != "" {
		return m.startWorkAgent(instruction)
	}
	if m.workAgent.active {
		m.notice = "Both-agent session is already active. Stop it before configuring a new session."
		return m, nil
	}
	if m.screen != workspaceScreen || m.workspace.Post.AcceptedBy == nil || (!m.isPoster(m.workspace.Post) && !m.isWorker(m.workspace.Post)) {
		m.notice = "Open your accepted task workspace first."
		return m, nil
	}
	m.commands.open = false
	if !m.workSetup.armed {
		m.workSetup = workSetup{open: true, busy: true, postID: m.workspace.Post.ID, nonce: rand.Text() + rand.Text(), generation: m.workSetup.generation + 1, instruction: instruction, autoReady: true, invite: true}
	} else {
		m.workSetup.open = true
	}
	generation := m.workSetup.generation
	return m, func() tea.Msg {
		config, err := providers.LoadConfig(m.profile)
		label := ""
		if err == nil && config.Active == "" {
			err = errors.New("Connect a model with /model first.")
		}
		if err == nil {
			_, err = providers.ConnectedClient(m.profile, config.Active)
			label = config.Active + " / " + config.Connections[config.Active].Model
		}
		var state api.AgentReadiness
		if err == nil {
			state, err = m.client.Readiness(m.workspaceCtx, m.token, m.workSetup.postID, nil, "")
		}
		return workSetupResult{generation: generation, readiness: state, provider: label, err: err}
	}
}
func (m model) workSetupPoll() tea.Cmd {
	generation := m.workSetup.generation
	return tea.Tick(5*time.Second, func(time.Time) tea.Msg { return workSetupTick{generation} })
}
func (m model) refreshWorkSetup() tea.Cmd {
	setup := m.workSetup
	status, detail := m.workStatus()
	return func() tea.Msg {
		var consent *bool
		if setup.armed {
			yes := true
			consent = &yes
		}
		var state api.AgentReadiness
		var err error
		if consent != nil {
			state, err = m.client.InviteReadiness(m.workspaceCtx, m.token, setup.postID, *consent, setup.nonce, setup.invite, setup.target, false, status, detail)
		} else {
			state, err = m.client.Readiness(m.workspaceCtx, m.token, setup.postID, nil, "")
		}
		return workSetupResult{generation: setup.generation, readiness: state, provider: setup.provider, err: err}
	}
}
func (m *model) withdrawReady() {
	if !m.workSetup.armed {
		return
	}
	setup, client, token := m.workSetup, m.client, m.token
	m.workSetup.armed = false
	m.workSetup.generation++
	if client == nil {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		no := false
		_, _ = client.Readiness(ctx, token, setup.postID, &no, setup.nonce)
	}()
}
func (m model) updateWorkSetup(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "r":
		if m.workSetup.err != nil && !m.workSetup.busy {
			instruction := m.workSetup.instruction
			m.withdrawReady()
			m.workSetup.armed = false
			return m.openWorkSetup(instruction)
		}
	case "esc":
		m.workSetup.open = false
	case "enter", "y":
		if m.workSetup.busy || m.workSetup.err != nil || !m.workSetup.prepared {
			return m, nil
		}
		if m.workspace.Escrow.State != "confirmed" {
			m.workSetup.err = errors.New("Confirm escrow funding before starting together.")
			return m, nil
		}
		if m.workspace.AgentControl.Mode == "manual" {
			m.workSetup.err = errors.New("Allow agent access in Agents first.")
			return m, nil
		}
		m.workSetup.armed, m.workSetup.busy = true, true
		return m, m.refreshWorkSetup()
	case "n":
		m.withdrawReady()
		m.workSetup.generation++
		m.workSetup.autoReady, m.workSetup.busy = false, false
		m.workSetup.open = false
	}
	return m, nil
}
func (m model) workSetupResult(msg workSetupResult) (tea.Model, tea.Cmd) {
	s := &m.workSetup
	if msg.generation != s.generation || m.screen != workspaceScreen || s.postID != m.workspace.Post.ID {
		return m, nil
	}
	s.busy, s.err, s.peer = false, msg.err, msg.readiness.Peer
	s.peerStatus, s.peerDetail = msg.readiness.PeerStatus, msg.readiness.PeerDetail
	if msg.readiness.You {
		if s.armed {
			s.invite = false
			s.target = ""
		}
	}
	if msg.provider != "" {
		s.provider, s.prepared = msg.provider, true
	}
	if msg.err != nil {
		if strings.Contains(msg.err.Error(), "another session is ready for this account") {
			s.err = errors.New("This account already has /work open in another terminal. Use the other participant's account there, or cancel the existing session first.")
		}
		if s.armed {
			m.stopWorkAgent("Coordination paused: " + msg.err.Error())
		}
		return m, nil
	}
	if s.autoReady && s.prepared && !s.armed {
		s.autoReady = false
		return m.updateWorkSetup(tea.KeyPressMsg{Code: tea.KeyEnter})
	}
	if s.armed && s.pair != "" && (msg.readiness.Pair != s.pair || !msg.readiness.Peer) {
		m.stopWorkAgent("Other agent stopped or disconnected. Open /work to coordinate again.")
		return m, m.saveLocalConversation()
	}
	if s.armed && msg.readiness.You && msg.readiness.Peer && !m.workAgent.active {
		s.pair, s.open = msg.readiness.Pair, false
		if m.invitations.pending != nil && m.invitations.pending.PostID == s.postID {
			m.invitations.pending = nil
		}
		next, start := m.startWorkAgent(s.instruction)
		m = next.(model)
		if !m.workAgent.active {
			m.withdrawReady()
			m.workSetup.open = true
			m.workSetup.err = errors.New(m.notice)
		}
		return m, tea.Batch(start, m.workSetupPoll())
	}
	if s.armed && msg.readiness.You && !msg.readiness.Peer {
		s.open = false
		m.notice = "Work invitation sent. Waiting for the other participant to Accept; you can keep using this workspace."
	}
	return m, m.workSetupPoll()
}
func (m model) workSetupView() tea.View {
	s := m.workSetup
	own, peer := "Preparing", "Invitation pending"
	if s.armed {
		own = "Ready · invitation sent"
	}
	if s.peer {
		peer = "Ready"
	}
	role := "worker"
	if m.isPoster(m.workspace.Post) {
		role = "requester"
	}
	role += " · @" + m.user.GitHubLogin + " · profile " + m.profile
	rows := []string{bold(fmt.Sprintf("Inviting participant · task #%d", m.workspace.Post.ID)), "", "Your role: " + role, "Model: " + plain(s.provider), "Folder: " + plain(m.localDirectory()), "Permissions: " + permissionModes[m.permissionMode()].name, "", "You: " + own, "Other participant: " + peer, "", "The other participant receives an Accept / Decline invitation.", "Both agents start after they accept and their setup is verified."}
	if s.err != nil {
		own, peer = "Setup failed", "Invitation not confirmed"
		rows[0] = bold(fmt.Sprintf("Invitation failed · task #%d", m.workspace.Post.ID))
		rows[7], rows[8] = "You: "+own, "Other participant: "+peer
		rows = append(rows, "", warning(plain(s.err.Error())))
	}
	footer := "Preparing invitation · n cancel · Esc workspace"
	if s.armed {
		footer = "Waiting for acceptance · n cancel invitation · Esc workspace"
	}
	if s.busy {
		footer = "Checking setup… · Esc back"
	}
	if s.err != nil && !s.busy {
		footer = "r retry · Esc workspace"
	}
	return m.localView("Agent coordination", rows, footer)
}

func (m model) workStatus() (string, string) {
	if !m.workAgent.active {
		return "ready", "Waiting for the other participant"
	}
	if m.workAgent.paused {
		return "paused", "Waiting for local Continue"
	}
	if m.localAgent.approval != nil {
		return "waiting_for_approval", "Waiting for local action approval"
	}
	if m.localAgent.busy {
		return "working", "Executing a task turn"
	}
	return "listening", "Listening for task updates"
}
