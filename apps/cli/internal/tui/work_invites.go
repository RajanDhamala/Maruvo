package tui

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/rajandhamala/Maruvo/cli/internal/api"
)

type workInvitation struct {
	PostID    int64  `json:"post_id"`
	Title     string `json:"title"`
	From      int64  `json:"from"`
	Nonce     string `json:"nonce"`
	Declined  bool   `json:"declined"`
	Cancelled bool   `json:"cancelled"`
	Status    string `json:"status"`
	Detail    string `json:"detail"`
}
type inviteState struct {
	selection  int
	ctx        context.Context
	stream     *api.WorkspaceStream
	cancel     context.CancelFunc
	generation uint64
	pending    *workInvitation
	accepted   *workInvitation
	busy       bool
}
type inviteConnected struct {
	generation uint64
	stream     *api.WorkspaceStream
	err        error
}
type inviteFrame struct {
	generation uint64
	frame      api.StreamFrame
	err        error
}
type inviteRetry struct{ generation uint64 }
type inviteWorkspace struct {
	generation uint64
	invitation workInvitation
	info       api.PostInfo
	err        error
}

func (m *model) stopInvites() {
	if m.invitations.cancel != nil {
		m.invitations.cancel()
	}
	m.invitations.stream.Close()
	m.invitations = inviteState{generation: m.invitations.generation + 1}
}
func (m model) connectInvites() tea.Cmd {
	generation, token, ctx := m.invitations.generation, m.token, m.invitations.ctx
	if ctx == nil {
		ctx = m.ctx
	}
	return func() tea.Msg {
		stream, err := m.client.ConnectWorkspace(ctx, token, 0, 0)
		return inviteConnected{generation, stream, err}
	}
}
func (m model) readInvite() tea.Cmd {
	generation, stream := m.invitations.generation, m.invitations.stream
	return func() tea.Msg { frame, err := stream.Read(); return inviteFrame{generation, frame, err} }
}
func (m model) inviteRetry() tea.Cmd {
	generation := m.invitations.generation
	return tea.Tick(3*time.Second, func(time.Time) tea.Msg { return inviteRetry{generation} })
}
func (m model) receiveInvite(msg inviteFrame) (tea.Model, tea.Cmd) {
	if msg.generation != m.invitations.generation {
		return m, nil
	}
	if msg.err != nil {
		m.invitations.stream.Close()
		m.invitations.stream = nil
		return m, m.inviteRetry()
	}
	var invitation workInvitation
	if (msg.frame.Event == "work.invite" || msg.frame.Event == "work.status") && json.Unmarshal(msg.frame.Data, &invitation) == nil {
		m.applyInvitation(invitation)
	}
	return m, m.readInvite()
}
func (m model) updateInvite(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	invitation := *m.invitations.pending
	key := msg.String()
	switch key {
	case "up", "shift+tab":
		m.invitations.selection = 0
		return m, nil
	case "down", "tab":
		m.invitations.selection = 1
		return m, nil
	case "enter":
		if m.invitations.selection == 1 {
			key = "n"
		} else {
			key = "y"
		}
	}
	switch key {
	case "n", "esc":
		m.invitations.pending = nil
		return m, func() tea.Msg {
			_, _ = m.client.InviteReadiness(m.ctx, m.token, invitation.PostID, false, rand.Text()+rand.Text(), false, invitation.Nonce, true)
			return nil
		}
	case "y", "enter":
		if m.localAgent.busy || m.workAgent.active || m.workSetup.armed {
			m.notice = "Stop the current agent session before accepting this invitation."
			return m, nil
		}
		if m.invitations.busy {
			return m, nil
		}
		m.invitations.busy = true
		generation := m.invitations.generation
		return m, func() tea.Msg {
			info, err := m.client.PostInfo(m.ctx, m.token, invitation.PostID)
			return inviteWorkspace{generation, invitation, info, err}
		}
	}
	return m, nil
}
func (m model) inviteView() tea.View {
	invitation := m.invitations.pending
	rows := []string{bold("Work invitation"), "", fmt.Sprintf("Participant #%d invited you to task #%d", invitation.From, invitation.PostID), plain(invitation.Title), "", "Accept opens this workspace and starts your agent together with theirs.", "Your local permission settings still apply. Payment signing remains separate.", "Folder: " + plain(m.localDirectory()), "Permissions: " + permissionModes[m.permissionMode()].name}
	if m.notice != "" {
		rows = append(rows, "", muted(plain(m.notice)))
	}
	for i, label := range []string{"Accept and open workspace", "Decline"} {
		prefix := "  "
		if i == m.invitations.selection {
			prefix = "› "
		}
		rows = append(rows, prefix+label)
	}
	return m.bottomPanel(rows, "↑↓ select · Enter confirm · Esc decline")
}

func (m *model) applyInvitation(invitation workInvitation) {
	if invitation.PostID <= 0 || len(invitation.Nonce) < 32 || len(invitation.Nonce) > 128 {
		return
	}
	if invitation.Status != "" {
		if m.workSetup.postID == invitation.PostID && (m.workSetup.target == invitation.Nonce || strings.HasPrefix(m.workSetup.pair, invitation.Nonce+":") || strings.HasSuffix(m.workSetup.pair, ":"+invitation.Nonce)) {
			m.workSetup.peerStatus, m.workSetup.peerDetail = invitation.Status, invitation.Detail
		}
		return
	}
	if invitation.Cancelled {
		if m.invitations.pending != nil && m.invitations.pending.PostID == invitation.PostID && m.invitations.pending.Nonce == invitation.Nonce {
			m.invitations.pending = nil
			m.notice = "The work invitation was cancelled."
		}
		if m.invitations.accepted != nil && m.invitations.accepted.PostID == invitation.PostID && m.invitations.accepted.Nonce == invitation.Nonce {
			m.invitations.accepted = nil
			m.workspacePendingAction = ""
		}
		if m.workSetup.postID == invitation.PostID && (m.workSetup.target == invitation.Nonce || strings.HasPrefix(m.workSetup.pair, invitation.Nonce+":") || strings.HasSuffix(m.workSetup.pair, ":"+invitation.Nonce)) {
			m.workSetup.peerStatus, m.workSetup.peerDetail = "stopped", "Other participant stopped the session"
			m.stopWorkAgent("Other participant stopped the remote-work session.")
			m.workSetup.generation++
			m.workSetup.autoReady, m.workSetup.open = false, false
		}
		return
	}
	if invitation.Declined {
		if m.workSetup.postID == invitation.PostID && m.workSetup.nonce == invitation.Nonce {
			m.stopWorkAgent("Other participant declined the work invitation.")
			m.workSetup.open = false
		}
		return
	}
	if !(m.workAgent.active && m.workAgent.postID == invitation.PostID) && !(m.workSetup.armed && m.workSetup.postID == invitation.PostID) {
		m.invitations.selection = 0
		m.invitations.pending = &invitation
	}
}
