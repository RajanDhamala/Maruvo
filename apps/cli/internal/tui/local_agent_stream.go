package tui

import (
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/rajandhamala/Maruvo/cli/internal/providers"
)

type localAgentTick struct{ sequence uint64 }

func (m model) tickLocalAgent() tea.Cmd {
	sequence := m.localAgent.sequence

	return tea.Tick(150*time.Millisecond, func(time.Time) tea.Msg {
		return localAgentTick{sequence: sequence}
	})
}

func (a *localAgentState) stopThinking() {
	if !a.thinkStarted.IsZero() && a.thinkTime == 0 {
		a.thinkTime = time.Since(a.thinkStarted)
	}
}

func (a *localAgentState) finishStream(interrupted bool) {
	a.stopThinking()

	if a.streaming && interrupted {
		a.lines[a.streamRow] = "Agent: " + strings.TrimPrefix(a.lines[a.streamRow], "Agent (streaming): ") +
			"\n\n_Response interrupted; incomplete answer._"
	}

	a.streaming = false
}

func (a *localAgentState) applyEvent(event providers.Event) bool {
	switch event.Type {
	case "response_start":
		a.thinking, a.thinkStarted, a.thinkTime = "", time.Time{}, 0
		a.thinkingAt, a.phase = len(a.lines), "Waiting for model"

		return false
	case "reasoning_start", "reasoning_delta":
		if a.thinkStarted.IsZero() {
			a.thinkStarted = time.Now()
		}

		a.thinking += event.Text
		a.phase = "Thinking"

		return false
	case "assistant_delta":
		a.stopThinking()

		if !a.streaming {
			a.streaming, a.streamRow = true, len(a.lines)
			a.lines = append(a.lines, "Agent (streaming): ", "")
		}

		a.lines[a.streamRow] += event.Text

		a.phase = "Answering"
		if time.Since(a.checkpoint) < time.Second {
			return false
		}

		a.checkpoint = time.Now()
	case "tool_preparing":
		a.stopThinking()
		a.phase = "Preparing tools"

		return false
	case "assistant":
		a.stopThinking()

		if a.streaming {
			a.lines[a.streamRow] = "Agent: " + event.Text
		} else {
			a.lines = append(a.lines, "Agent: "+event.Text, "")
		}

		a.streaming, a.phase = false, "Finishing response"
		a.chat.Messages = append(
			a.chat.Messages,
			providers.ChatMessage{Role: "assistant", Content: event.Text},
		)
	case "usage":
		a.usage = event.Text
	default:
		a.stopThinking()

		a.phase = "Using tools"
		if event.Status != "running" {
			a.phase = "Waiting for model"
		}

		row, exists := a.toolRows[event.ToolID]
		if event.ToolID != "" && event.Status != "running" && exists {
			a.lines[row] = "Tool: " + event.Text
		} else {
			if event.ToolID != "" {
				if a.toolRows == nil {
					a.toolRows = make(map[string]int)
				}

				a.toolRows[event.ToolID] = len(a.lines)
			}

			a.lines = append(a.lines, "Tool: "+event.Text)
		}

		if event.Status == "succeeded" && strings.Contains(event.Text, "post #") &&
			(strings.HasPrefix(event.Text, "✓ Create post") || strings.HasPrefix(event.Text, "✓ Accept task")) {
			a.postsChanged = true
		}
	}

	return true
}

func (a localAgentState) progressText() string {
	if a.started.IsZero() {
		return "Connecting model…"
	}

	frames := []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

	return fmt.Sprintf("%s %s · %s · Esc stop", frames[a.frame%len(frames)], a.phase,
		time.Since(a.started).Truncate(time.Second))
}
