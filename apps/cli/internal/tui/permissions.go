package tui

import (
	tea "charm.land/bubbletea/v2"
	"crypto/sha256"
	"fmt"
	"github.com/rajandhamala/Maruvo/cli/internal/providers"
	"sync"
)

type localPermissions struct {
	confirmSelection            int
	remembered                  *sync.Map
	open, confirm, full         bool
	account, profile, directory string
	selection, mode             int
}

var permissionModes = []struct{ name, description string }{
	{"Ask for approval", "Ask before commands, edits or file transfers."},
	{"Approve for me", "Allow project edits; ask before commands or file transfers."},
	{"Full Access", "Allow commands, edits and transfers without prompts; unsandboxed."},
	{"Read Only", "Read files by default; ask for each change, command or transfer."},
}

func (m model) permissionMode() int {
	p := m.permissions
	if p.account != m.user.ID || p.profile != m.profile || p.directory != m.localDirectory() {
		return 0
	}
	if p.full {
		return 2
	}
	return p.mode
}

func (m model) permitsAction(action string) bool {
	mode := m.permissionMode()
	if m.permissions.remembered != nil {
		if v, ok := m.permissions.remembered.Load(m.approvalKey(providers.Approval{Action: "__policy__"})); ok {
			mode = v.(int)
		}
	}
	return mode == 2 || (mode == 1 && action == "")
}

func (m *model) setPermissionMode(mode int) {
	p := &m.permissions
	if p.remembered != nil {
		p.remembered.Range(func(k, v any) bool { p.remembered.Delete(k); return true })
		p.remembered.Store(m.approvalKey(providers.Approval{Action: "__policy__"}), mode)
	}
	p.mode, p.full, p.open, p.confirm = mode, mode == 2, false, false
	p.account, p.profile, p.directory = m.user.ID, m.profile, m.localDirectory()
	m.notice = "Agent permissions: " + permissionModes[mode].name
}

func (m model) hasFullAccess() bool {
	p := m.permissions
	return p.full && p.account == m.user.ID && p.profile == m.profile && p.directory == m.localDirectory()
}

func (m model) openPermissions() (tea.Model, tea.Cmd) {
	if m.localAgent.busy && m.localAgent.approval == nil {
		m.notice = "Stop the task agent and finish the current turn before changing permissions."
		return m, nil
	}
	if m.localAgent.approval == nil && (m.workAgent.active || m.workSetup.armed) {
		m.stopWorkAgent("Paused to update local permissions. Confirm Ready again afterward.")
	}
	m.commands.open = false
	m.permissions.open, m.permissions.confirm = true, false
	m.permissions.selection = m.permissionMode()
	return m, nil
}

func (m model) updatePermissions(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	p := &m.permissions
	if p.confirm {
		switch msg.String() {
		case "up", "shift+tab":
			p.confirmSelection = 0
		case "down", "tab":
			p.confirmSelection = 1
		case "esc", "n":
			p.open, p.confirm = false, false
		case "y":
			m.setPermissionMode(2)
		case "enter":
			if p.confirmSelection == 0 {
				m.setPermissionMode(2)
			} else {
				p.open, p.confirm = false, false
			}
		}
		return m, nil
	}
	switch msg.String() {
	case "up":
		p.selection = max(0, p.selection-1)
	case "down", "tab":
		p.selection = min(len(permissionModes)-1, p.selection+1)
	case "1", "2", "3", "4":
		p.selection = int(msg.String()[0] - '1')
	case "enter":
		if p.confirm {
			return m, nil
		}
		if p.selection == 2 {
			p.confirm = true
			p.confirmSelection = 1
		} else {
			m.setPermissionMode(p.selection)
		}
	case "esc", "n":
		p.open, p.confirm = false, false
	case "a":
		m.setPermissionMode(0)
	case "f":
		p.confirm = true
		p.confirmSelection = 1
	case "y":
		if p.confirm {
			m.setPermissionMode(2)
			m.notice = "Full access approved for this account and folder until the CLI exits."
		}
	}
	return m, nil
}

func (m model) permissionsView() tea.View {
	width, _ := m.dimensions()
	rows := []string{bold("Update Model Permissions"), "", "Account: " + plain(m.user.Username) + " · " + plain(m.profile), "Folder: " + plain(m.localDirectory()), ""}
	for i, mode := range permissionModes {
		label := fmt.Sprintf("%d. %s", i+1, mode.name)
		if i == m.permissionMode() {
			label += " (current)"
		}
		row := fmt.Sprintf("%-30s %s", label, mode.description)
		if width < 100 {
			row = label
		}
		if i == m.permissions.selection {
			row = accent("› " + row)
		} else {
			row = muted("  " + row)
		}
		rows = append(rows, row)
	}
	if width < 100 {
		rows = append(rows, "", permissionModes[m.permissions.selection].description)
	}
	footer := "↑↓ select · Enter apply · Esc back"
	if m.permissions.confirm {
		rows = []string{warning("Allow full access on this device?"), "", "Account: " + plain(m.user.Username) + " · " + plain(m.profile), "Folder: " + plain(m.localDirectory()), "", "Commands can read/write outside the project and use the network.", "Sensitive files readable by your OS user are accessible.", "No OS sandbox or root elevation. Payments require human signing.", "Approval is local and lasts for this session/account/folder only."}
		for i, label := range []string{"Yes, allow full access for this session", "No, keep current permissions"} {
			prefix := "  "
			if i == m.permissions.confirmSelection {
				prefix = "› "
			}
			rows = append(rows, prefix+label)
		}
		footer = "↑↓ select · Enter confirm · Esc cancel"
	}
	return m.bottomPanel(rows, footer)
}

func (m model) approvalKey(change providers.Approval) [32]byte {
	return sha256.Sum256([]byte(fmt.Sprintf("%q", []string{m.user.ID, m.profile, m.localDirectory(), change.Action})))
}
func (m model) rememberedApproval(change providers.Approval) bool {
	if m.permissions.remembered == nil {
		return false
	}
	_, ok := m.permissions.remembered.Load(m.approvalKey(change))
	return ok
}
