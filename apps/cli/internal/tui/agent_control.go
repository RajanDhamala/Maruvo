package tui

import (
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/rajandhamala/Maruvo/cli/internal/api"
)

type agentControls struct {
	open, busy bool
	generation uint64
	selection  int
	confirm    string
	grantID    string
	control    api.AgentControl
	grants     []api.AgentGrant
	err        error
	notice     string
}

type agentControlsLoaded struct {
	generation, workspaceGen uint64
	token                    string
	postID                   int64
	control                  api.AgentControl
	grants                   []api.AgentGrant
	err                      error
	notice                   string
}

func (m model) openAgentControls() (tea.Model, tea.Cmd) {
	m.agentControls = agentControls{open: true, busy: true, generation: m.agentControls.generation + 1}

	return m, m.fetchAgentControls("", "")
}

func (m model) fetchAgentControls(mode, grantID string) tea.Cmd {
	generation, workspaceGen := m.agentControls.generation, m.workspaceGen
	ctx, client, token, postID := m.workspaceCtx, m.client, m.token, m.workspace.Post.ID

	return func() tea.Msg {
		result := agentControlsLoaded{
			generation:   generation,
			workspaceGen: workspaceGen,
			token:        token,
			postID:       postID,
		}
		if grantID != "" {
			_, result.err = client.RevokeAgentGrant(ctx, token, grantID)
			if result.err == nil {
				result.notice = "Selected agent access revoked."
			}
		}

		if result.err != nil {
			return result
		}

		if mode == "" {
			result.control, result.err = client.AgentControl(ctx, token, postID)
		} else {
			result.control, result.err = client.SetAgentControl(ctx, token, postID, mode)
			if result.err == nil {
				result.notice = "Agent access allowed. Revoked credentials remain revoked."
				if mode == "manual" {
					result.notice = fmt.Sprintf(
						"Manual control enabled. Revoked %d agent grants.",
						result.control.RevokedGrants,
					)
				}
			}
		}

		if result.err == nil {
			result.grants, result.err = client.AgentGrants(ctx, token, postID)
		}

		return result
	}
}

func newerAgentControl(current, incoming api.AgentControl) bool {
	return current.UpdatedAt != nil &&
		(incoming.UpdatedAt == nil || current.UpdatedAt.After(*incoming.UpdatedAt))
}

func (m model) agentControlsLoaded(msg agentControlsLoaded) (tea.Model, tea.Cmd) {
	if !m.agentControls.open || m.screen != workspaceScreen || msg.generation != m.agentControls.generation ||
		msg.workspaceGen != m.workspaceGen || msg.postID != m.workspace.Post.ID || msg.token != m.token {
		return m, nil
	}

	c := &m.agentControls

	c.busy, c.confirm, c.grantID, c.err, c.notice = false, "", "", msg.err, msg.notice
	if msg.control.Mode != "" {
		if newerAgentControl(m.workspace.AgentControl, msg.control) {
			c.busy = true
			return m, m.fetchAgentControls("", "")
		}

		c.control, m.workspace.AgentControl = msg.control, msg.control
	}

	if msg.err == nil {
		c.grants = msg.grants
		c.selection = min(c.selection, max(0, len(c.grants)-1))
	}

	return m, m.setPostError(msg.err)
}

func activeAgentGrant(grant api.AgentGrant, now time.Time) bool {
	return grant.RevokedAt == nil && grant.ExpiresAt.After(now)
}

func (m model) updateAgentControls(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	c := &m.agentControls

	key := msg.String()
	if key == "esc" {
		if c.confirm != "" {
			c.confirm, c.grantID = "", ""
		} else {
			c.open = false
		}

		return m, nil
	}

	if c.busy {
		return m, nil
	}

	if c.confirm != "" {
		if key == "n" {
			c.confirm, c.grantID = "", ""
		} else if key == "enter" || key == "y" {
			c.busy = true

			mode := c.confirm
			if mode == "revoke" {
				mode = ""
			}

			return m, m.fetchAgentControls(mode, c.grantID)
		}

		return m, nil
	}

	switch key {
	case "r":
		c.busy = true
		return m, m.fetchAgentControls("", "")
	case "up":
		c.selection = max(0, c.selection-1)
	case "down":
		c.selection = min(max(0, len(c.grants)-1), c.selection+1)
	case "m", "a":
		if c.control.Mode != "" {
			c.confirm = "manual"
			if key == "a" {
				c.confirm = "agent"
			}
		}
	case "x":
		if len(c.grants) > 0 && activeAgentGrant(c.grants[c.selection], time.Now()) {
			c.confirm, c.grantID = "revoke", c.grants[c.selection].ID
		}
	}

	return m, nil
}

func (m model) agentControlsView() tea.View {
	c := m.agentControls

	mode := "Checking agent access..."
	if c.control.Mode == "manual" {
		mode = "Manual control · agent actions paused"
	} else if c.control.Mode == "agent" {
		mode = "Agent access allowed"
	}

	rows := []string{bold(mode), muted("Your access for task #" + fmt.Sprint(m.workspace.Post.ID)), ""}
	footer := "m take over · a allow agents · ↑↓ select · x revoke · r refresh · Esc back"

	if c.confirm != "" {
		switch c.confirm {
		case "manual":
			rows = append(rows, bold("Take over this task?"),
				"Your task grants will be revoked and new grants blocked.",
				"You can still message, share files and review work.",
				"Other participants keep their own agent access.")
		case "agent":
			rows = append(rows, bold("Allow agent access again?"),
				"New task grants will be allowed.", "Revoked credentials will stay revoked.",
				"Restart your harness if it has stopped.")
		case "revoke":
			rows = append(rows, bold("Revoke the selected agent's access?"),
				"This credential will stop working for this task.",
				"Other grants and your own access stay available.")
		}

		footer = "Enter / y confirm · Esc / n cancel"
	} else {
		_, height := m.dimensions()
		count := max(1, height-13)

		start := max(0, c.selection-count+1)
		for i := start; i < min(len(c.grants), start+count); i++ {
			grant := c.grants[i]

			state := "active"
			if grant.RevokedAt != nil {
				state = "revoked"
			} else if !grant.ExpiresAt.After(time.Now()) {
				state = "expired"
			}

			label := plain(grant.Name) + " · " + state
			if i == c.selection {
				rows = append(rows, accent("› "+label))
			} else {
				rows = append(rows, "  "+label)
			}
		}

		if len(c.grants) == 0 && !c.busy {
			rows = append(rows, muted("No agent grants for this task."))
		}

		if len(c.grants) > 0 {
			grant := c.grants[c.selection]
			rows = append(rows, "", "Permissions: "+plain(strings.Join(grant.Permissions, ", ")),
				"Expires: "+grant.ExpiresAt.Local().Format("2006-01-02 15:04"))
		}
	}

	if c.err != nil {
		rows = append(rows, warning(plain(c.err.Error())))
	} else if c.busy {
		rows = append(rows, muted("Updating agent access..."))
	} else if c.notice != "" {
		rows = append(rows, muted(c.notice))
	}

	return m.localView("Human controls", rows, footer)
}
