package tui

import (
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/rajandhamala/Maruvo/cli/internal/api"
	"github.com/rajandhamala/Maruvo/cli/internal/providers"
)

type workspaceAgent struct {
	active, pending bool
	paused          bool
	postID          int64
	generation      uint64
	turns           int
	started         time.Time
	status          string
	runID           string
	instruction     string
}
type workspaceAgentTick struct{ generation uint64 }

func (m model) continueWorkAgent() (tea.Model, tea.Cmd) {
	a := &m.workAgent
	if m.screen != workspaceScreen || !a.active || !a.paused || a.postID != m.workspace.Post.ID ||
		!m.localAgent.open || m.localAgent.busy || m.localAgent.approval != nil || !errors.Is(m.localAgent.err, providers.ErrToolBatchPaused) {
		m.notice = "Your task agent has no paused tool batch."
		return m, nil
	}
	if m.workspace.Post.Status == "completed" || m.workspace.Post.Status == "cancelled" || m.workspace.AgentControl.Mode == "manual" {
		m.stopWorkAgent("Task agent stopped: this task is closed or agent access is disabled.")
		return m, m.saveLocalConversation()
	}
	if a.turns >= 20 || time.Since(a.started) >= 30*time.Minute {
		m.stopWorkAgent("Task agent limit reached (20 turns / 30 minutes). Use /work to resume.")
		return m, m.saveLocalConversation()
	}
	if m.localAgent.client == nil || m.localAgent.marketplace == nil || m.localAgent.marketplace.TaskID != a.postID {
		m.notice = "Stop and set up your task agent again before continuing."
		return m, nil
	}
	m.commands.open = false
	m.localAgent.input = textField{limit: 12000, byteLimit: 16000, multiline: true}
	if a.instruction != "" {
		m.localAgent.input.insert("Continue from the last verified tool result without repeating completed actions.\nLocal user's instruction: " + a.instruction)
		a.instruction = ""
	}
	a.turns++
	a.status = "Working · resuming tool batch"
	m.notice = "Your task agent is continuing."
	return m.sendLocalPrompt()
}

func (m model) workAgentTick() tea.Cmd {
	generation := m.workAgent.generation
	return tea.Tick(2*time.Second, func(time.Time) tea.Msg { return workspaceAgentTick{generation} })
}

func (m *model) stopWorkAgent(reason string) {
	m.withdrawReady()
	if m.workAgent.active {
		a := &m.localAgent
		if a.cancel != nil {
			a.cancel()
		}
		a.finishStream(true)
		a.sequence++
		a.open, a.busy, a.autoSend = false, false, false
		a.approval, a.answer, a.approvalGuidance = nil, nil, nil
		a.approvalEditing = false
		a.fundingTask, a.workspaceTask = 0, 0
		if a.chat.ID != "" {
			a.history = providers.ConversationHistory(a.chat)
		}
	}
	m.workAgent.active, m.workAgent.pending = false, false
	m.workAgent.generation++
	m.workAgent.status = reason
	m.notice = reason
	if m.localAgent.marketplace != nil {
		marketplace := *m.localAgent.marketplace
		marketplace.TaskID, marketplace.FundingUI = 0, true
		m.localAgent.marketplace = &marketplace
	}
}

func (m model) startWorkAgent(instructions ...string) (tea.Model, tea.Cmd) {
	instruction := ""
	if len(instructions) > 0 {
		instruction = strings.TrimSpace(instructions[0])
	}
	if m.screen != workspaceScreen || m.workspace.Post.AcceptedBy == nil || (!m.isPoster(m.workspace.Post) && !m.isWorker(m.workspace.Post)) {
		m.notice = "Open your accepted task workspace first."
		return m, nil
	}
	if m.workAgent.active {
		if instruction != "" {
			m.workAgent.instruction, m.workAgent.pending = instruction, true
			m.notice = "Instruction queued for the task agent."
			return m, nil
		}
		m.notice = "Task agent is already running."
		return m, nil
	}
	if m.localAgent.busy {
		m.notice = "Finish the current agent turn first."
		return m, nil
	}
	if m.workspace.AgentControl.Mode == "manual" {
		m.notice = "Allow agent access in Agents before starting."
		return m, nil
	}
	if m.workspace.Post.Status == "completed" || m.workspace.Post.Status == "cancelled" {
		m.notice = "This task is closed."
		return m, nil
	}
	save := m.saveLocalConversation()
	m.workAgent = workspaceAgent{active: true, pending: true, runID: rand.Text() + rand.Text(), postID: m.workspace.Post.ID, generation: m.workAgent.generation + 1, started: time.Now(), status: "Starting · model usage applies"}
	m.workAgent.instruction = instruction
	m.localAgent.history, m.localAgent.lines = nil, nil
	m.localAgent.chat = providers.Conversation{}
	next, ready := m.openLocalAgent()
	m = next.(model)
	// Provider setup completes before the first task turn starts.
	return m, tea.Batch(save, ready, m.workAgentTick())
}

func (m *model) wakeWorkAgent(event api.WorkspaceEvent) {
	a := &m.workAgent
	if !a.active || event.PostID != a.postID {
		return
	}
	if event.Kind == "agent.control" && m.workspace.AgentControl.Mode == "manual" {
		m.stopWorkAgent("Task agent stopped by manual takeover.")
		return
	}
	if m.workspace.Post.Status == "completed" || m.workspace.Post.Status == "cancelled" {
		m.stopWorkAgent("Task agent stopped: task closed.")
		return
	}
	switch event.Kind {
	case "message", "file.shared":
		var data struct {
			AgentName string `json:"agent_name"`
		}
		_ = json.Unmarshal(event.Data, &data)
		if event.ActorID != nil && *event.ActorID == parseUser(m.user.ID) && (data.AgentName != "" || event.Kind == "file.shared") {
			return
		}
		a.pending = true
	case "work.submitted", "review.changes_requested":
		if event.ActorID != nil && *event.ActorID == parseUser(m.user.ID) {
			return
		}
		a.pending = true
	case "escrow.updated", "task.status":
		a.pending = true
	}
}

func (m model) advanceWorkAgent(msg workspaceAgentTick) (tea.Model, tea.Cmd) {
	a := &m.workAgent
	if !a.active || msg.generation != a.generation || m.screen != workspaceScreen || m.workspace.Post.ID != a.postID {
		return m, nil
	}
	if (a.turns >= 20 && !m.localAgent.busy) || time.Since(a.started) >= 30*time.Minute {
		m.stopWorkAgent("Task agent limit reached (20 turns / 30 minutes). Use /work to resume.")
		return m, m.saveLocalConversation()
	}
	if m.commands.open || m.providers.open || m.agentControls.open || m.permissions.open ||
		m.workSetup.open || m.shortcutsOpen || m.invitations.pending != nil {
		return m, m.workAgentTick()
	}
	if a.paused {
		if m.hasFullAccess() {
			next, cmd := m.continueWorkAgent()
			m = next.(model)
			return m, tea.Batch(cmd, m.workAgentTick())
		}
		return m, m.workAgentTick()
	}
	if m.localAgent.err != nil && !m.localAgent.busy {
		m.stopWorkAgent("Task agent paused: " + m.localAgent.err.Error())
		return m, m.saveLocalConversation()
	}
	if !a.pending || m.localAgent.busy || m.localAgent.client == nil || m.commands.open || m.providers.open || m.agentControls.open {
		return m, m.workAgentTick()
	}
	if m.localAgent.marketplace == nil {
		m.stopWorkAgent("Task agent needs a signed-in account.")
		return m, nil
	}
	a.pending = false
	a.turns++
	a.status = fmt.Sprintf("Working · turn %d/20", a.turns)
	marketplace := *m.localAgent.marketplace
	marketplace.TaskID, marketplace.FundingUI = a.postID, false
	m.localAgent.marketplace = &marketplace
	m.localAgent.open = true
	m.localAgent.input = textField{limit: 12000, byteLimit: 16000, multiline: true}
	prompt := fmt.Sprintf("Continue authorized collaboration for task #%d. Read get_workspace for fresh state first. Perform the next permitted action for our role, or identify a concrete blocker. Publish substantive updates through send_message; remain silent when there is nothing new. Do not repeat prior updates.", a.postID)
	if a.instruction != "" {
		prompt += "\nLocal user's instruction: " + a.instruction
		a.instruction = ""
	}
	m.localAgent.input.insert(prompt)
	next, turn := m.sendLocalPrompt()
	m = next.(model)
	return m, tea.Batch(turn, m.workAgentTick())
}
