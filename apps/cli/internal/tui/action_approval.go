package tui

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
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
			choice = 2
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
			a.approvalChoice = min(4, a.approvalChoice+1)
		case "y":
			choice = 0
		case "n", "esc":
			choice = 2
		case "1", "2", "3", "4", "5":
			a.approvalChoice = int(msg.String()[0] - '1')
		case "enter":
			if a.approvalChoice == 3 {
				a.approvalEditing = true
				return m, nil
			}
			if a.approvalChoice == 4 {
				return m.openPermissions()
			}
			choice = a.approvalChoice
		case "pgup":
			a.approvalScroll = max(0, a.approvalScroll-5)
		case "pgdown":
			a.approvalScroll += 5
		}
	}
	if choice >= 0 && a.answer != nil {
		remember := choice == 1 && a.approval != nil && m.permissions.remembered != nil
		var key [32]byte
		if remember {
			key = m.approvalKey(*a.approval)
			m.permissions.remembered.Store(key, true)
		}
		select {
		case a.answer <- choice != 2:
			a.approval, a.answer = nil, nil
			a.approvalChoice, a.approvalScroll = 2, 0
		default:
			if remember {
				m.permissions.remembered.Delete(key)
			}
		}
	}
	return m, nil
}

func (m model) actionApprovalView() tea.View {
	a := m.localAgent
	approval := a.approval
	action := approval.Action
	if action == "" {
		action = "Edit file"
	}
	rows := []string{bold("Allow this action?"), "", "Action: " + plain(action), "Target: " + plain(approval.Path), ""}
	for i, label := range []string{"Yes, once", "Yes, don’t ask again for this action type this session", "No", "No, tell the model what to do instead", "Change permissions (/allow)"} {
		prefix := "  "
		if i == a.approvalChoice {
			prefix = "› "
			label = accent(label)
		}
		rows = append(rows, prefix+label)
	}
	if a.approvalEditing {
		rows = []string{bold("Suggest another approach"), "The pending action will be declined. Tell the model what to do instead:", a.approvalInput.inputPlaceholder(max(12, m.width-4), true, "Type guidance, or /allow to change permissions")}
		return m.bottomPanel(rows, "Enter send guidance · Esc back")
	}
	rows = append(rows, "", muted("Requested action details:"))
	width, height := m.dimensions()
	preview := strings.Split(ansi.Wrap(ansi.Strip(approval.Content), max(12, width-4), ""), "\n")
	available := max(1, min(16, height-5)-len(rows)-1)
	start := min(a.approvalScroll, max(0, len(preview)-available))
	end := min(len(preview), start+available)
	rows = append(rows, preview[start:end]...)
	if len(preview) > available {
		rows = append(rows, muted(fmt.Sprintf("Details %d–%d of %d · PgUp/PgDn", start+1, end, len(preview))))
	}
	return m.bottomPanel(rows, "↑↓ select · Enter confirm · Esc decline · PgUp/PgDn details")
}
