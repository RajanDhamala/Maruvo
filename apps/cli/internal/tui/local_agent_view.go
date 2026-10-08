package tui

import (
	"regexp"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/rajandhamala/Maruvo/cli/internal/providers"
)

func (m model) localAgentLayout() postLayout {
	width, height := m.dimensions()
	inner := max(12, width-4)
	body := max(1, height-5)

	inputHeight := 5
	if height < 22 {
		inputHeight = 3
	}

	a := m.localAgent
	rows := make([]string, body)

	rows[0], rows[1] = muted(a.label), muted("Folder: "+m.localDirectory())
	if a.label == "" && a.chat.ID != "" {
		rows[0] = muted("Saved chat · " + plain(a.chat.Provider+" / "+a.chat.Model))
	}

	var usage []string
	if a.usage != "" {
		usageText := a.usage
		if height < 22 {
			parts := strings.Split(usageText, " · ")
			if len(parts) > 2 {
				usageText = strings.Join(parts[:2], " · ") + " …"
			}
		}

		usage = strings.Split(ansi.Wrap(plain(usageText), inner, ""), "\n")

		limit := max(1, body-inputHeight-7)
		if len(usage) > limit {
			usage = usage[:limit]
			usage[limit-1] = ansi.Truncate(usage[limit-1], inner-1, "") + "…"
		}
	}

	inputY := body - inputHeight
	statusY := inputY - len(usage) - 1
	pickerRows, pickerHits := m.agentFileRows(inner, max(1, statusY-4))
	pickerY := statusY - len(pickerRows)
	visible := max(1, pickerY-3)
	transcript := m.localAgentTranscript(min(100, inner))
	end := len(transcript) - min(a.scroll, max(0, len(transcript)-visible))
	start := max(0, end-visible)
	copy(rows[3:pickerY], transcript[start:end])
	copy(rows[pickerY:statusY], pickerRows)

	status := muted("File edits ask for approval.")
	switch {
	case a.err != nil:
		status = warning(plain(a.err.Error()))
	case a.storageErr != nil:
		status = warning("Chat history: " + plain(a.storageErr.Error()))
	case a.approval != nil:
		action := a.approval.Action
		if action == "" {
			action = "Apply"
		}

		status = accent(plain(action) + " " + plain(a.approval.Path) + "? y / n")
	case a.busy:
		status = accent(a.progressText())
	case a.status != "":
		status = muted(a.status)
	}

	rows[statusY] = status
	for i, row := range usage {
		rows[statusY+1+i] = muted(row)
	}

	prompt := make([]string, inputHeight)
	for i := range prompt {
		prompt[i] = inputRow("", inner-1)
	}

	inputStart := 0
	if inputHeight == 5 {
		inputStart = 1
	}

	placeholder, submit, meta := "Ask the agent… · @ files · / commands", " Enter send ", "@ files · / commands"
	if a.busy {
		placeholder, submit = "Model responding… Esc stops the request.", " Working… "
		meta = "Input locked · Esc stop"
	}

	inputRows, _ := a.input.textRows(inner-1, max(1, inputHeight-3),
		!a.busy && a.approval == nil, placeholder)
	copy(prompt[inputStart:], inputRows)

	prompt[inputHeight-2] = inputRow(
		" "+align(muted(meta), button(submit, !a.busy), inner-4),
		inner-1,
	)
	for i, row := range prompt {
		rows[inputY+i] = accent("▎") + row
	}

	layout := postLayout{rows: rows, footer: m.shortcutHint()}

	for _, hit := range pickerHits {
		hit.x, hit.y = 2, 2+pickerY+hit.y
		layout.hits = append(layout.hits, hit)
	}

	if inner >= 70 {
		rows[0] = align(rows[0], button(" History ", false), inner)
		layout.hit(2+inner-9, 2, 9, 1, "agent-history", 0)
	}

	layout.hit(2, 2+inputY+inputStart, inner, max(1, inputHeight-3), "agent-input", 0)
	layout.hit(2+inner-14, 2+inputY+inputHeight-2, 12, 1, "agent-send", 0)

	return layout
}

func (m model) localAgentView() tea.View {
	width, height := m.dimensions()
	if width < 48 || height < 16 {
		return m.frame(nil, "")
	}

	if m.localAgent.sessions.open {
		layout := m.localSessionsLayout()
		view := m.localView("Resume a previous session", layout.rows, layout.footer)
		view.MouseMode = tea.MouseModeCellMotion

		return view
	}

	layout := m.localAgentLayout()
	view := m.localView("CLI agent", layout.rows, layout.footer)
	view.MouseMode = tea.MouseModeCellMotion

	return view
}

func (m model) localAgentTranscript(width int) []string {
	render := m.localAgent.render
	if render == nil {
		render = &localAgentRender{}
	}

	var rows []string

	for index, line := range m.localAgent.lines {
		if index == m.localAgent.thinkingAt {
			rows = append(rows, m.localThinkingRows(width)...)
		}

		if content, user := strings.CutPrefix(line, "You: "); user {
			for _, row := range strings.Split(ansi.Wrap(ansi.Strip(content), width-4, ""), "\n") {
				rows = append(rows, inputRow(" "+row, width))
			}

			continue
		}

		content, assistant := strings.CutPrefix(line, "Agent: ")

		partial, streaming := strings.CutPrefix(line, "Agent (streaming): ")
		if streaming {
			content, assistant = partial, true
		}

		if assistant {
			rows = append(rows, m.localAssistantHeading())

			if streaming {
				stable, tail := stableAgentMarkdown(content)
				if stable != "" {
					rows = append(rows, render.markdown(index, stable, width)...)
					if tail != "" {
						rows = append(rows, "")
					}
				}

				rows = append(rows, agentStreamingRows(tail, width)...)
			} else {
				rows = append(rows, render.markdown(index, content, width)...)
			}

			continue
		}

		if content, tool := strings.CutPrefix(line, "Tool: "); tool {
			wrapped := strings.Split(ansi.Wrap(ansi.Strip(content), width-2, ""), "\n")
			for i, row := range wrapped {
				if i == 0 {
					if strings.HasPrefix(content, "×") {
						rows = append(rows, warning(row))
					} else {
						rows = append(rows, accent(row))
					}
				} else {
					rows = append(rows, muted("  "+strings.TrimSpace(row)))
				}
			}

			continue
		}

		rows = append(rows, strings.Split(ansi.Wrap(ansi.Strip(line), width, ""), "\n")...)
	}

	if m.localAgent.thinkingAt == len(m.localAgent.lines) {
		rows = append(rows, m.localThinkingRows(width)...)
	}

	if approval := m.localAgent.approval; approval != nil {
		rows = append(rows, accent("Proposed file: "+plain(approval.Path)))
		rows = append(rows, strings.Split(ansi.Wrap(ansi.Strip(approval.Content), width, ""), "\n")...)
	}

	for len(rows) > 0 && strings.TrimSpace(ansi.Strip(rows[len(rows)-1])) == "" {
		rows = rows[:len(rows)-1]
	}

	return rows
}

func (m model) localAssistantHeading() string {
	a := m.localAgent

	provider, name := a.provider, a.model
	if name == "" {
		provider, name = a.chat.Provider, a.chat.Model
	}

	if name == "" {
		return accent("◆ MARUVO")
	}

	return accent("◆ "+(providers.Model{ID: name}).DisplayName()) + muted(" · "+plain(provider))
}

func (m model) localThinkingRows(width int) []string {
	a := m.localAgent
	if a.thinking == "" {
		return nil
	}

	elapsed := a.thinkTime

	label := "Thought"
	if elapsed == 0 {
		elapsed, label = time.Since(a.thinkStarted), "Thinking"
	}

	toggle := "expand"
	if a.thinkOpen {
		toggle = "collapse"
	}

	rows := []string{muted(ansi.Truncate("◇ "+label+" · "+elapsed.Truncate(time.Second).String()+
		" · Ctrl+o "+toggle, width, "…"))}
	if !a.thinkOpen {
		return rows
	}

	text := strings.Split(ansi.Wrap(strings.TrimSpace(ansi.Strip(a.thinking)), width-4, ""), "\n")

	for _, line := range text {
		rows = append(rows, muted("│ "+line))
	}

	return rows
}

var agentListMarker = regexp.MustCompile(`^(\s*)(?:• |[0-9]+\. )`)

func agentMarkdownRows(rendered string, width int) []string {
	var rows []string

	listIndent := 0
	code := false

	for _, row := range strings.Split(strings.Trim(rendered, "\n"), "\n") {
		plain := ansi.Strip(row)

		row = ansi.Truncate(row, ansi.StringWidth(strings.TrimRight(plain, " ")), "")
		switch strings.TrimSpace(plain) {
		case "╭─ Code":
			code, listIndent = true, 0

			rows = append(rows, accent("╭─ Code"))

			continue
		case "╰─":
			code = false

			rows = append(rows, muted("╰─"))

			continue
		}

		if code {
			prefix := "│ "
			if plain != "" && !strings.HasPrefix(plain, " ") {
				prefix = "│ ↪ "
			}

			for _, part := range strings.Split(ansi.Wrap(row, width-4, ""), "\n") {
				rows = append(rows, muted(prefix)+part)
				prefix = "│ ↪ "
			}

			continue
		}

		if strings.TrimSpace(plain) == "" {
			listIndent = 0

			rows = append(rows, "")

			continue
		}

		if marker := agentListMarker.FindString(plain); marker != "" {
			listIndent = ansi.StringWidth(marker)
		} else if listIndent > 0 {
			leading := len(plain) - len(strings.TrimLeft(plain, " "))
			if leading < listIndent {
				row = strings.Repeat(" ", listIndent-leading) + row
			}
		}

		if ansi.StringWidth(row) <= width {
			rows = append(rows, row)
			continue
		}

		wrapped := strings.Split(ansi.Wrap(row, max(12, width-listIndent), ""), "\n")

		rows = append(rows, wrapped[0])
		for _, continuation := range wrapped[1:] {
			rows = append(rows, strings.Repeat(" ", listIndent)+continuation)
		}
	}

	return rows
}

func (m model) updateLocalAgentMouse(msg tea.MouseClickMsg) (tea.Model, tea.Cmd) {
	if m.localAgent.sessions.open {
		return m.updateLocalSessionMouse(msg)
	}

	width, height := m.dimensions()
	if width < 48 || height < 16 || msg.Button != tea.MouseLeft || m.localAgent.busy ||
		m.localAgent.approval != nil {
		return m, nil
	}

	for _, hit := range m.localAgentLayout().hits {
		if msg.X < hit.x || msg.X >= hit.x+hit.width || msg.Y < hit.y || msg.Y >= hit.y+hit.height {
			continue
		}

		if hit.action == "agent-send" {
			return m.sendLocalPrompt()
		}

		if hit.action == "agent-history" {
			return m.openLocalSessions()
		}

		if hit.action == "agent-file" {
			m.localAgent.files.selection = hit.index
			return m.selectAgentFile()
		}

		lines, cursor := m.localAgent.input.textLines(hit.width - 1)
		start := max(0, cursor-hit.height+1)
		row := min(len(lines)-1, start+msg.Y-hit.y)
		m.localAgent.input.cursorAt(hit.width-1, row, max(0, msg.X-hit.x-1))

		return m, m.completeAgentInput()
	}

	return m, nil
}
