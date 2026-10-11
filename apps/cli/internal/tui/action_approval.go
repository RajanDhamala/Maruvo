package tui

import (
	"fmt"
	"strings"
	"sync"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/rajandhamala/Maruvo/cli/internal/providers"
)

const (
	approvalAccept = iota
	approvalNo
	approvalAll
)

func (m model) updateActionApproval(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	a := &m.localAgent
	choice := -1
	if a.approvalEditing {
		switch msg.String() {
		case "esc":
			a.approvalEditing = false
			return m, nil
		case "enter":
			text := strings.TrimSpace(a.approvalInput.value)
			if text == "" {
				return m, nil
			}
			if text == "/allow" {
				a.approvalEditing = false
				return m.openPermissions()
			}
			if a.approvalGuidance != nil {
				*a.approvalGuidance = text
			}
			a.approvalEditing = false
			choice = approvalNo
		default:
			a.approvalInput.key(msg)
			return m, nil
		}
	}
	if choice < 0 {
		switch msg.String() {
		case "up", "left", "shift+tab":
			a.approvalChoice = max(0, a.approvalChoice-1)
		case "down", "right", "tab":
			a.approvalChoice = min(approvalAll, a.approvalChoice+1)
		case "y":
			choice = approvalAccept
		case "n", "esc":
			choice = approvalNo
		case "1", "2", "3":
			a.approvalChoice = int(msg.String()[0] - '1')
		case "g":
			a.approvalEditing = true
		case "a":
			return m.openPermissions()
		case "enter":
			choice = a.approvalChoice
		case "pgup":
			a.approvalScroll = max(0, a.approvalScroll-5)
		case "pgdown":
			a.approvalScroll += 5
		}
	}
	if choice >= 0 && a.answer != nil {
		allowAll := choice == approvalAll && a.approval != nil
		var key [32]byte
		var previous any
		var existed bool
		if allowAll {
			if m.permissions.remembered == nil {
				m.permissions.remembered = &sync.Map{}
			}
			key = m.approvalKey(providers.Approval{Action: "__policy__"})
			previous, existed = m.permissions.remembered.Load(key)
			m.permissions.remembered.Store(key, 2)
		}
		select {
		case a.answer <- choice != approvalNo:
			if allowAll {
				m.setPermissionMode(2)
			}
			a.approval, a.answer = nil, nil
			a.approvalChoice, a.approvalScroll = approvalNo, 0
			if m.workAgent.active {
				m.workAgent.status = fmt.Sprintf("Working · turn %d/20", m.workAgent.turns)
			}
		default:
			if allowAll {
				if existed {
					m.permissions.remembered.Store(key, previous)
				} else {
					m.permissions.remembered.Delete(key)
				}
			}
		}
	}
	return m, nil
}

func (m model) actionApprovalView() tea.View {
	rows, footer := m.actionApprovalRows()
	return m.bottomPanel(rows, footer)
}

func (m model) updateActionApprovalMouse(msg tea.MouseClickMsg) (tea.Model, tea.Cmd) {
	width, height := m.dimensions()
	if msg.Button != tea.MouseLeft || m.localAgent.approvalEditing || msg.X < 2 || msg.X >= width-2 {
		return m, nil
	}
	rows, footer := m.actionApprovalRows()
	panel, rowIndexes := m.bottomPanelRows(rows, footer)
	y := msg.Y - (height - len(panel))
	if y < 0 || y >= len(rowIndexes) {
		return m, nil
	}
	choice := rowIndexes[y] - 5
	if choice < approvalAccept || choice > approvalAll {
		return m, nil
	}
	m.localAgent.approvalChoice = choice
	return m.updateActionApproval(tea.KeyPressMsg{Code: tea.KeyEnter})
}

func (m model) actionApprovalRows() ([]string, string) {
	a := m.localAgent
	approval := a.approval
	action := approval.Action
	if action == "" {
		action = "Edit file"
	}
	rows := []string{bold("Allow this action?"), "", "Action: " + plain(action), "Target: " + plain(approval.Path), ""}
	for i, label := range []string{"Accept once", "No", "Allow all for this session"} {
		prefix := "  "
		if i == a.approvalChoice {
			prefix = "› "
			label = accent(label)
		}
		rows = append(rows, prefix+label)
	}
	if a.approvalEditing {
		rows = []string{bold("Suggest another approach"), "The pending action will be declined. Tell the model what to do instead:", a.approvalInput.inputPlaceholder(max(12, m.width-4), true, "Type guidance, or /allow to change permissions")}
		return rows, "Enter send guidance · Esc back"
	}
	rows = append(rows, "", muted("All: commands, edits and transfers. Payments stay manual."), muted("Requested action details:"))
	width, height := m.dimensions()
	preview := strings.Split(ansi.Wrap(ansi.Strip(approval.Content), max(12, width-4), ""), "\n")
	available := max(1, min(16, height-5)-len(rows)-1)
	start := min(a.approvalScroll, max(0, len(preview)-available))
	end := min(len(preview), start+available)
	rows = append(rows, preview[start:end]...)
	if len(preview) > available {
		rows = append(rows, muted(fmt.Sprintf("Details %d–%d of %d · PgUp/PgDn", start+1, end, len(preview))))
	}
	return rows, "↑↓ select · Enter confirm · Esc No · g guidance · a permissions · PgUp/PgDn details"
}
