package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/rajandhamala/Maruvo/cli/internal/providers"
)

func TestAgentStreamBatchesTextAndFlushesBeforeControlEvents(t *testing.T) {
	m := streamingFixture(t)
	for _, event := range []providers.Event{
		{Type: "reasoning_delta", Text: "Check "}, {Type: "reasoning_delta", Text: "files"},
		{Type: "tool_preparing"},
		{Type: "assistant_delta", Text: "Hello "}, {Type: "assistant_delta", Text: "world"},
		{Type: "assistant", Text: "Hello world"},
	} {
		m.localAgent.events <- localAgentUpdate{sequence: 4, event: event}
	}

	first := m.waitLocalAgent()().(localAgentUpdate)
	if len(first.leading) != 1 || first.leading[0].Text != "Check files" ||
		first.event.Type != "tool_preparing" {
		t.Fatal("reasoning chunks were not batched before tool progress")
	}

	next, _ := m.Update(first)

	m = next.(model)
	if m.localAgent.thinking != "Check files" || m.localAgent.phase != "Preparing tools" {
		t.Fatal("batched reasoning or tool progress was lost")
	}

	second := m.waitLocalAgent()().(localAgentUpdate)
	if len(second.leading) != 1 || second.leading[0].Text != "Hello world" ||
		second.event.Type != "assistant" {
		t.Fatal("answer chunks were not flushed before the final answer")
	}

	next, _ = m.Update(second)

	m = next.(model)
	if m.localAgent.streaming || len(m.localAgent.chat.Messages) != 2 || len(m.localAgent.lines) != 4 ||
		m.localAgent.lines[2] != "Agent: Hello world" {
		t.Fatal("batched final answer duplicated or dropped text")
	}
}

func TestAgentMarkdownKeepsFinishedBlocksCachedDuringStreaming(t *testing.T) {
	m := streamingFixture(t)
	m.localAgent.lines = []string{"You: Hello", "", "Agent: **Old** answer.", "", "You: Next", ""}
	m.localAgent.thinkingAt = len(m.localAgent.lines)
	m.localAgent.applyEvent(
		providers.Event{Type: "assistant_delta", Text: "**New** paragraph.\n\n```go\nfmt"},
	)
	m.localAgentTranscript(80)
	renderer := m.localAgent.render.renderer
	old := m.localAgent.render.lines[2]
	live := m.localAgent.render.lines[6]
	m.localAgent.applyEvent(providers.Event{Type: "assistant_delta", Text: ".Println(\"hi\")"})

	transcript := strings.Join(m.localAgentTranscript(80), "\n")
	if m.localAgent.render.renderer != renderer || m.localAgent.render.lines[6].text != live.text ||
		&m.localAgent.render.lines[2].rows[0] != &old.rows[0] {
		t.Fatal("an unfinished chunk recreated the renderer or reformatted a completed block")
	}

	if !strings.Contains(ansi.Strip(transcript), `fmt.Println("hi")`) ||
		strings.Contains(
			ansi.Strip(transcript),
			"**Old**",
		) || strings.Contains(ansi.Strip(transcript), "**New**") {
		t.Fatal("stable Markdown or streamed code tail was lost")
	}

	m.localAgent.thinking = strings.Repeat("Raw changing thought text ", 50)
	if rows := m.localThinkingRows(80); len(rows) != 1 || strings.Contains(rows[0], "Raw changing") {
		t.Fatal("collapsed thinking should be a stable single row")
	}
}

func TestStableAgentMarkdownDoesNotCommitAnOpenFenceOrSingleNewline(t *testing.T) {
	for _, text := range []string{"Partial paragraph\n", "```go\ncode\n\n", "````go\n```\n\n"} {
		if prefix, tail := stableAgentMarkdown(text); prefix != "" || tail != text {
			t.Fatalf("unfinished Markdown committed prematurely: %q", text)
		}
	}

	text := "Ready.\n\n```go\ncode\n\n"
	if prefix, tail := stableAgentMarkdown(text); prefix != "Ready.\n\n" || tail != "```go\ncode\n\n" {
		t.Fatal("blank lines inside an open fence changed the stable paragraph boundary")
	}
}
