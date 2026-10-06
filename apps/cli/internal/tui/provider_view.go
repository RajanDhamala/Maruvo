package tui

import (
	"strings"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/rajandhamala/Maruvo/cli/internal/providers"
)

func (m model) providerLayout() postLayout {
	p := m.providers
	width, height := m.dimensions()
	popupWidth := min(72, width-4)
	inside := popupWidth - 4
	title, group, footer := "Connect a provider", "Providers", "↑↓ choose · Enter connect"

	field, placeholder := p.query, "Search"
	if p.step == providerKey {
		title, footer = "API key", "Enter submit"
		field, placeholder = p.key, "API key"
		field.value = strings.Repeat("•", utf8.RuneCountInString(field.value))
	} else if p.step == providerModel {
		title, group, footer = "Select a model", providerLabel(
			providers.Names[p.provider],
		), "↑↓ choose · Enter save"
	}

	input := field.render(inside, !p.busy)
	if field.value == "" {
		input = muted(placeholder)
		if !p.busy {
			input = "\x1b[7m" + placeholder[:1] + "\x1b[0m" + muted(placeholder[1:])
		}
	}

	l := postLayout{rows: []string{"", align(bold(title), muted("esc"), inside), "", input, ""}}
	l.hit(popupWidth-5, 1, 3, 1, "provider-back", 0)
	l.hit(2, 3, inside, 1, "provider-input", 0)

	if p.step == providerKey {
		label := providerLabel(providers.Names[p.provider]) + " · encrypted locally"
		if connection, ok := p.config.Connections[providers.Names[p.provider]]; ok && p.key.value == "" {
			if connection.Storage == "encrypted" {
				label = "Saved key available · Enter to reuse"
			} else {
				label = "Enter your key to encrypt this connection."
			}
		}

		l.rows = append(l.rows, muted(label))
	} else {
		l.rows = append(l.rows, accent(group))

		indices := m.providerIndices()
		if p.step == providerModel {
			indices = m.providerModelIndices()
		}

		visible := min(12, max(1, height-12))

		start := max(0, p.selection-visible+1)
		for row := start; row < min(len(indices), start+visible); row++ {
			index := indices[row]
			label, action := "", "provider-choice"

			if p.step == providerChoose {
				name := providers.Names[index]

				mark := "  "
				if connection, ok := p.config.Connections[name]; ok {
					mark = "✓ "
					if connection.Storage != "encrypted" {
						mark = "! "
					}
				}

				label = mark + providerLabel(name)
			} else {
				label, action, index = "  "+p.models[index].ID, "provider-model", row
			}

			label = ansi.Truncate(label, inside, "…")
			if row == p.selection {
				label = "\x1b[1;30;46m" + label + strings.Repeat(
					" ",
					inside-ansi.StringWidth(label),
				) + "\x1b[0m"
			}

			l.hit(2, len(l.rows), inside, 1, action, index)
			l.rows = append(l.rows, label)
		}

		if len(indices) == 0 {
			l.rows = append(l.rows, muted("No matches. Clear the search to try again."))
		}
	}

	if p.busy {
		status := "Checking provider…"
		if p.saving {
			status = "Saving encrypted key…"
		} else if p.step == providerChoose {
			status = "Loading connections…"
		}

		l.rows = append(l.rows, muted(status))
	} else if p.err != nil {
		message := strings.Split(ansi.Wrap(plain(p.err.Error()), inside, ""), "\n")
		for _, line := range message[:min(2, len(message))] {
			l.rows = append(l.rows, warning(line))
		}
	}

	l.rows = append(l.rows, "")
	l.hit(2, len(l.rows), min(inside, ansi.StringWidth(footer)), 1, "provider-submit", 0)

	l.rows = append(l.rows, muted(footer), "")
	for i, row := range l.rows {
		l.rows[i] = dashboardPanelRow("  "+ansi.Truncate(row, inside, "…")+"  ", popupWidth)
	}

	return l
}

func (m model) providerArea() hitArea {
	width, height := m.dimensions()
	popupWidth := min(72, width-4)
	popupHeight := len(m.providerLayout().rows)

	return hitArea{x: (width - popupWidth) / 2, y: max(0, (height-popupHeight)/2),
		width: popupWidth, height: popupHeight}
}

func (m model) providersView() tea.View {
	width, height := m.dimensions()
	if width < 48 || height < 16 {
		return m.frame(nil, "")
	}

	background := m
	background.providers.open, background.commands.open = false, false
	view := background.View()

	lines := strings.Split(view.Content, "\n")
	for len(lines) < height {
		lines = append(lines, "")
	}

	for i := range lines {
		lines[i] = "\x1b[38;2;65;65;65;48;2;10;10;10m" + ansi.Strip(lines[i]) + "\x1b[0m"
	}

	layout := m.providerLayout()
	drawOverlay(width, lines, m.providerArea(), layout.rows)
	view.SetContent(strings.Join(lines, "\n"))
	view.MouseMode = tea.MouseModeCellMotion
	view.Cursor = nil

	return view
}

func (m model) updateProviderMouse(msg tea.MouseClickMsg) (tea.Model, tea.Cmd) {
	if msg.Button != tea.MouseLeft {
		return m, nil
	}

	width, height := m.dimensions()
	if width < 48 || height < 16 {
		return m, nil
	}

	area := m.providerArea()

	x, y := msg.X-area.x, msg.Y-area.y
	if x < 0 || x >= area.width || y < 0 || y >= area.height {
		return m.closeProviders()
	}

	for _, hit := range m.providerLayout().hits {
		if x < hit.x || x >= hit.x+hit.width || y < hit.y || y >= hit.y+hit.height {
			continue
		}

		if hit.action == "provider-back" {
			return m.backProvider()
		}

		if m.providers.busy {
			return m, nil
		}

		switch hit.action {
		case "provider-choice":
			return m.chooseProvider(hit.index)
		case "provider-model":
			m.providers.selection = hit.index
			return m.saveProvider()
		case "provider-submit":
			return m.updateProviders(tea.KeyPressMsg{Code: tea.KeyEnter})
		case "provider-input":
			field := &m.providers.query
			if m.providers.step == providerKey {
				field = &m.providers.key
			}

			visible := *field
			if m.providers.step == providerKey {
				visible.value = strings.Repeat("•", utf8.RuneCountInString(field.value))
			}

			field.cursor = visible.visibleStart(hit.width)

			column, runes := max(0, x-hit.x), []rune(visible.value)
			for field.cursor < len(runes) {
				cells := ansi.StringWidth(string(runes[field.cursor]))
				if column < cells {
					break
				}

				column -= cells
				field.cursor++
			}
		}
	}

	return m, nil
}
