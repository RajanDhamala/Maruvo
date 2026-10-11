package tui

import (
	"regexp"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

var hintSeparator = regexp.MustCompile(`\s{2,}| · `)

func shortcutParts(hint string) []string {
	var parts []string

	for _, part := range hintSeparator.Split(ansi.Strip(hint), -1) {
		if part = strings.TrimSpace(part); part != "" {
			parts = append(parts, part)
		}
	}

	return parts
}

func compactHint(hint string, width int) string {
	parts := shortcutParts(hint)
	if len(parts) == 0 {
		return "F1 help"
	}

	primary := 0

	for i, part := range parts {
		if strings.HasPrefix(part, "Enter") || strings.HasPrefix(part, "Ctrl+s") ||
			strings.HasPrefix(part, "Tab / Enter") {
			primary = i
			break
		}

		if strings.Contains(parts[primary], "scroll") || strings.HasPrefix(parts[primary], "↑") ||
			strings.HasPrefix(parts[primary], "Arrows") {
			primary = i
		}
	}

	action := parts[primary]
	switch action {
	case "Enter / y approve funding":
		action = "Enter sign funding"
	case "Enter / y sign decision":
		action = "Enter sign decision"
	}

	secondary := ""

	for i, part := range parts {
		if i != primary && strings.Contains(part, "Esc") {
			secondary = part
			break
		}
	}

	if secondary == "" {
		for i, part := range parts {
			if i != primary && !strings.HasPrefix(part, "↑") && !strings.Contains(part, "scroll") {
				secondary = part
				break
			}
		}
	}

	suffix := " · F1 help"
	if secondary != "" {
		candidate := " · " + secondary + suffix
		if ansi.StringWidth(candidate)+8 <= width {
			suffix = candidate
		}
	}

	return ansi.Truncate(action, max(1, width-ansi.StringWidth(suffix)), "…") + suffix
}

func (m model) shortcutHint() string {
	switch {
	case m.agentControls.open:
		if m.agentControls.confirm != "" {
			return "Enter / y confirm · Esc / n cancel"
		}

		return "m take over · a allow agents · ↑↓ select · x revoke selected grant · r refresh · Esc back"
	case m.remote.open:
		return "Tab view · ↑↓ select · Enter delegate / open · r refresh · Esc close"
	case m.providers.open:
		if m.providers.busy {
			return "Esc cancel provider request"
		}

		switch m.providers.step {
		case providerKey:
			return "Enter submit · Esc back"
		case providerModel:
			return "Type to search · ↑↓ choose · Enter select · Ctrl+d disconnect · Esc back"
		case providerOptions:
			return "Tab / ↑↓ next setting · ←→ adjust · Enter save · Ctrl+d disconnect · Esc back"
		default:
			return "Type to search · ↑↓ choose · Enter connect · Ctrl+d disconnect · Esc close"
		}
	case m.commands.open:
		if m.commands.directory {
			return "↑↓ choose · Tab browse · Enter open · Esc cancel"
		}

		return "Type to search · ↑↓ choose · Enter select · Esc close"
	case m.localAgent.open:
		if m.localAgent.sessions.open {
			return "Enter resume · ↑↓ / PgUp/PgDn choose · Type search · Tab select filter · ←→ change · Ctrl+f Cwd/All · Ctrl+s Active/Archived · Ctrl+o Updated/Created · Ctrl+d archive/restore · Ctrl+n new chat · Esc back"
		}

		if m.localAgent.approval != nil {
			return "y apply edit · n decline · Esc stop request"
		}

		if m.localAgent.busy {
			return "Esc stop request · Ctrl+o thinking · PgUp/PgDn history"
		}

		if m.localAgent.files.open {
			return "↑↓ select · Tab / Enter insert · Esc close files"
		}

		return "Enter send · Shift+Enter newline · Ctrl+r saved chats · Ctrl+o thinking · PgUp/PgDn / wheel history · Ctrl+n new conversation · Esc back"
	case m.picker.open:
		return "Arrows select · PgUp/PgDn month · Tab time · Enter apply · Esc cancel"
	case m.profileOpen:
		return "w connect wallet · g connect GitHub · Enter / l log out · Esc close"
	case m.demo:
		return "Enter / r send again · q quit"
	case m.token == "":
		return "Tab / Shift+Tab select · Enter continue · / commands · Ctrl+g GitHub · Ctrl+o Google"
	case m.onDashboard():
		return m.dashboardShortcuts()
	case m.screen == newPostScreen:
		if m.form.importing || m.form.timingOpen {
			return m.formLayout().footer
		}

		return m.formLayout().footer + " · Ctrl+d timings · Ctrl+o description file"
	default:
		hint := m.postsLayout().footer
		if m.composing() && !m.composer.completing {
			hint += " · Shift+Enter newline · Ctrl+s submit · Ctrl+Backspace remove attachment · PgUp/PgDn history"
		}

		return hint
	}
}

func (m model) shortcutRows() []string {
	rows := []string{bold("This screen"), ""}
	for _, part := range shortcutParts(m.shortcutHint()) {
		rows = append(rows, part)
	}

	rows = append(rows, "", bold("General"), "", "F1 show / close shortcuts", "Ctrl+c quit")
	if m.token != "" && !m.demo && !m.providers.open && !m.localAgent.open && !m.commands.open &&
		!m.picker.open {
		rows = append(rows, "Ctrl+p account", "Ctrl+g connect GitHub")
	}

	return rows
}

func (m model) shortcutLines() []string {
	width, _ := m.dimensions()

	var wrapped []string
	for _, row := range m.shortcutRows() {
		wrapped = append(wrapped, strings.Split(ansi.Wrap(row, max(1, width-4), ""), "\n")...)
	}

	return wrapped
}

func (m model) shortcutsView() tea.View {
	_, height := m.dimensions()
	wrapped := m.shortcutLines()
	visible := max(1, height-5)
	start := min(m.shortcutScroll, max(0, len(wrapped)-visible))

	view := m.localView(
		"Keyboard shortcuts",
		wrapped[start:min(len(wrapped), start+visible)],
		"↑↓ scroll · Esc close",
	)
	view.MouseMode = tea.MouseModeCellMotion

	return view
}

func (m model) updateShortcuts(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	_, height := m.dimensions()
	limit := max(0, len(m.shortcutLines())-max(1, height-5))
	m.shortcutScroll = min(m.shortcutScroll, limit)

	switch msg.String() {
	case "esc", "f1":
		m.shortcutsOpen, m.shortcutScroll = false, 0
	case "up":
		m.shortcutScroll = max(0, m.shortcutScroll-1)
	case "down":
		m.shortcutScroll = min(limit, m.shortcutScroll+1)
	case "pgup":
		m.shortcutScroll = max(0, m.shortcutScroll-max(1, height-6))
	case "pgdown":
		m.shortcutScroll = min(limit, m.shortcutScroll+max(1, height-6))
	}

	return m, nil
}
