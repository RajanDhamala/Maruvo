package tui

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/rajandhamala/Maruvo/cli/internal/providers"
)

func streamingFixture(t *testing.T) model {
	t.Helper()
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	m := dashboardFixture()
	m.token = ""

	chat, err := providers.NewConversation(t.TempDir(), "Hello", "deepseek", "deepseek-flash")
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)

	chat.Pending = true
	chat.Messages = []providers.ChatMessage{{Role: "user", Content: "Hello"}}
	m.localAgent = localAgentState{
		open: true, busy: true, sequence: 4, client: &providers.Client{},
		provider: "deepseek", model: "deepseek-flash", directory: chat.Directory,
		chat: chat, chatScope: providers.ChatScope{Profile: "default", Account: "7"},
		ctx: ctx, cancel: cancel, events: make(chan localAgentUpdate, 16),
		lines: []string{"You: Hello", ""}, thinkingAt: 2,
		started: time.Now(), checkpoint: time.Now(), phase: "Waiting for model",
		input:  textField{limit: 12000, byteLimit: 16000, multiline: true},
		render: &localAgentRender{},
	}

	return m
}

func TestStreamUpdatesOneAnswerAndKeepsReasoningOutOfSavedChats(t *testing.T) {
	m := streamingFixture(t)
	for _, event := range []providers.Event{
		{Type: "response_start"},
		{Type: "reasoning_delta", Text: "PRIVATE provider reasoning"},
		{Type: "assistant_delta", Text: "Hello "},
		{Type: "assistant_delta", Text: "**world**"},
	} {
		next, _ := m.Update(localAgentUpdate{sequence: 4, event: event})
		m = next.(model)
	}

	if len(m.localAgent.chat.Messages) != 1 || len(m.localAgent.lines) != 4 ||
		m.localAgent.lines[2] != "Agent (streaming): Hello **world**" || m.localAgent.phase != "Answering" {
		t.Fatal("stream fragments must update one pending answer without finalizing it")
	}

	if result := m.saveLocalConversation()().(localConversationSaved); result.err != nil {
		t.Fatal(result.err)
	}

	chat, err := providers.LoadConversation(
		m.localAgent.chatScope,
		m.localAgent.chat.Directory,
		m.localAgent.chat.ID,
	)

	data, _ := json.Marshal(chat)
	if err != nil || strings.Contains(string(data), "PRIVATE") || !chat.Pending || len(chat.Messages) != 1 {
		t.Fatal("saved checkpoint must exclude ephemeral reasoning and keep the answer pending")
	}

	next, _ := m.Update(
		localAgentUpdate{sequence: 4, event: providers.Event{Type: "assistant", Text: "Hello **world**"}},
	)

	m = next.(model)
	if len(m.localAgent.lines) != 4 || len(m.localAgent.chat.Messages) != 2 || m.localAgent.streaming ||
		m.localAgent.lines[2] != "Agent: Hello **world**" {
		t.Fatal("final response duplicated or lost the streamed answer")
	}

	transcript := strings.Join(m.localAgentTranscript(100), "\n")
	if !strings.Contains(transcript, "\x1b[48;5;236m") || strings.Contains(transcript, "YOU") ||
		!strings.Contains(transcript, "DeepSeek V4.1 Flash") || strings.Contains(transcript, "Agent:") ||
		strings.Contains(transcript, "**world**") {
		t.Fatal("user card, model identity or assistant Markdown was lost")
	}
}

func TestStoppingStreamMarksPartialAndIgnoresLateEvents(t *testing.T) {
	m := streamingFixture(t)
	m.localAgent.applyEvent(providers.Event{Type: "assistant_delta", Text: "Half an answer"})
	next, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})

	m = next.(model)
	if m.localAgent.busy || m.localAgent.streaming || !m.localAgent.open || m.localAgent.ctx.Err() == nil ||
		!strings.Contains(
			m.localAgent.lines[2],
			"incomplete answer",
		) || len(m.localAgent.chat.Messages) != 1 {
		t.Fatal("stopped stream must retain an explicitly incomplete answer")
	}

	next, _ = m.Update(
		localAgentUpdate{sequence: 4, event: providers.Event{Type: "assistant_delta", Text: "late text"}},
	)

	m = next.(model)
	if strings.Contains(strings.Join(m.localAgent.lines, ""), "late text") {
		t.Fatal("late streaming event changed a stopped conversation")
	}

	_, cmd := m.Update(localAgentTick{sequence: 4})
	if cmd != nil {
		t.Fatal("stale spinner clock restarted after cancellation")
	}
}

func TestThinkingPreviewExpandsAndFitsSmallTerminals(t *testing.T) {
	m := streamingFixture(t)
	m.localAgent.applyEvent(
		providers.Event{Type: "reasoning_delta", Text: strings.Repeat("Checking the project details.\n", 12)},
	)
	compact := len(m.localThinkingRows(100))
	next, _ := m.Update(tea.KeyPressMsg{Code: 'o', Mod: tea.ModCtrl})

	m = next.(model)
	if !m.localAgent.thinkOpen || len(m.localThinkingRows(100)) <= compact {
		t.Fatal("Ctrl+o did not expand provider reasoning")
	}

	next, _ = m.Update(tea.KeyPressMsg{Code: 'o', Mod: tea.ModCtrl})

	m = next.(model)
	for _, size := range [][2]int{{48, 16}, {80, 24}, {120, 36}} {
		m.width, m.height = size[0], size[1]

		view := m.View().Content
		if !strings.Contains(ansi.Strip(view), "Thinking") || !strings.Contains(view, "Working") ||
			len(strings.Split(view, "\n")) != size[1] {
			t.Fatalf("thinking/input hidden or wrong terminal height at %v", size)
		}

		for _, line := range strings.Split(view, "\n") {
			if ansi.StringWidth(line) > size[0] {
				t.Fatalf("streaming UI overflowed at %v", size)
			}
		}
	}

	frame := m.localAgent.frame

	next, cmd := m.Update(localAgentTick{sequence: 4})
	if next.(model).localAgent.frame != frame+1 || cmd == nil {
		t.Fatal("busy indicator did not animate")
	}
}
