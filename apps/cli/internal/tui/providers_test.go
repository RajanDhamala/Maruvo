package tui

import (
	"context"
	"errors"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/rajandhamala/Maruvo/cli/internal/providers"
)

func TestProviderPickerMasksKeysAndPreservesTaskDraft(t *testing.T) {
	m := dashboardFixture()
	m.homeInput = textField{value: "Keep this task draft", limit: 2000}
	next, _ := m.openProviders()
	m = next.(model)
	m.providers.busy, m.providers.step = false, providerKey
	next, _ = m.Update(tea.PasteMsg{Content: "private-provider-secret"})

	m = next.(model)
	if m.providers.key.value != "private-provider-secret" || m.homeInput.value != "Keep this task draft" {
		t.Fatal("provider paste must stay in its own key field")
	}

	if view := ansi.Strip(
		m.View().Content,
	); strings.Contains(view, "private-provider-secret") ||
		!strings.Contains(view, "•••") {
		t.Fatal("provider view exposed the API key")
	}

	m.providers.secret = "previous-secret"
	m.providers.models = []providers.Model{{ID: "previous-model"}}
	next, _ = m.Update(
		providerModelsLoaded{sequence: m.providers.sequence, err: errors.New("provider unavailable")},
	)

	m = next.(model)
	if m.providers.secret != "" || len(m.providers.models) != 0 {
		t.Fatal("failed provider checks must discard previous credentials and models")
	}

	next, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	next, _ = next.Update(tea.KeyPressMsg{Code: tea.KeyEscape})

	m = next.(model)
	if m.providers.open || m.providers.key.value != "" || m.homeInput.value != "Keep this task draft" {
		t.Fatal("closing the provider picker must clear secrets and preserve the task draft")
	}

	next, _ = m.Update(providerModelsLoaded{sequence: m.providers.sequence - 1, secret: "late-secret"})
	if next.(model).providers.secret != "" {
		t.Fatal("late model responses must not restore credentials")
	}
}

func TestLocalAgentApprovalAndCancellation(t *testing.T) {
	m := dashboardFixture()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	answer := make(chan bool, 1)

	m.localAgent = localAgentState{
		open: true, busy: true, sequence: 3, ctx: ctx, cancel: cancel,
		approval: &providers.Approval{Path: "output.md", Content: strings.Repeat("Proposed content\n", 80)},
		answer:   answer,
	}
	for _, size := range [][2]int{{48, 16}, {80, 24}, {120, 36}} {
		m.width, m.height = size[0], size[1]

		view := ansi.Strip(m.View().Content)
		if !strings.Contains(view, "Apply output.md? y / n") {
			t.Fatalf("approval path and controls hidden at %dx%d", size[0], size[1])
		}

		for _, line := range strings.Split(view, "\n") {
			if ansi.StringWidth(line) > size[0] {
				t.Fatal("local agent view exceeds terminal width")
			}
		}
	}

	next, _ := m.Update(tea.KeyPressMsg{Code: 'n', Text: "n"})

	m = next.(model)
	if <-answer || m.localAgent.approval != nil {
		t.Fatal("declined approval must return false and clear the proposed edit")
	}

	next, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})

	m = next.(model)
	if ctx.Err() == nil || m.localAgent.open {
		t.Fatal("Esc must cancel the request and close the agent")
	}

	next, _ = m.Update(localAgentUpdate{sequence: 3, event: providers.Event{Type: "assistant", Text: "late"}})
	if len(next.(model).localAgent.lines) != 0 {
		t.Fatal("late agent events must be ignored")
	}
}

func TestProviderPickerStagesSearchAndModelSelection(t *testing.T) {
	m := dashboardFixture()
	m.homeInput = textField{value: "Keep the task draft", limit: 2000}
	next, _ := m.openProviders()
	m = next.(model)
	m.providers.busy = false
	next, _ = m.Update(tea.PasteMsg{Content: "router"})

	m = next.(model)
	if indices := m.providerIndices(); len(indices) != 1 || indices[0] != 1 {
		t.Fatal("provider search must filter to OpenRouter")
	}

	next, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})

	m = next.(model)
	if m.providers.step != providerKey || m.providers.provider != 1 || m.providers.query.value != "" {
		t.Fatal("provider selection must open a separate empty API-key dialog")
	}

	next, _ = m.Update(tea.PasteMsg{Content: "private-key"})
	m = next.(model)
	next, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})

	m = next.(model)
	if cmd == nil || !m.providers.busy {
		t.Fatal("API-key submission must start provider validation")
	}

	next, _ = m.Update(providerModelsLoaded{sequence: m.providers.sequence,
		models: []providers.Model{{ID: "first-model"}, {ID: "other-model"}}, secret: "private-key"})

	m = next.(model)
	if m.providers.step != providerModel || m.providers.key.value != "" ||
		m.providers.secret != "private-key" {
		t.Fatal("validated keys must proceed to model selection and clear the key input")
	}

	next, _ = m.Update(tea.PasteMsg{Content: "other"})

	m = next.(model)
	if indices := m.providerModelIndices(); len(indices) != 1 || indices[0] != 1 {
		t.Fatal("model search must filter the available model IDs")
	}

	next, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})

	m = next.(model)
	if m.providers.step != providerKey || m.providers.key.value != "private-key" || m.providers.secret != "" {
		t.Fatal("Esc from models must return to the masked key input")
	}

	next, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})

	m = next.(model)
	if m.providers.step != providerChoose || m.providers.key.value != "" || m.providers.secret != "" {
		t.Fatal("Esc from the key dialog must return to providers and clear credentials")
	}

	next, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})

	m = next.(model)
	if m.providers.open || m.homeInput.value != "Keep the task draft" {
		t.Fatal("closing the flow must preserve the task draft")
	}
}

func TestProviderDialogsFitAndKeepBackgroundVisible(t *testing.T) {
	for _, signedIn := range []bool{false, true} {
		for _, size := range [][2]int{{48, 16}, {80, 24}, {120, 36}, {240, 60}} {
			m := dashboardFixture()
			if !signedIn {
				m.token = ""
			}

			m.width, m.height = size[0], size[1]
			m.homeInput = textField{value: "Keep this task draft", limit: 2000}

			m.providers = providerSettings{
				open:   true,
				key:    textField{value: "private-key", cursor: 11, limit: 4096},
				query:  textField{limit: 200},
				models: []providers.Model{{ID: "test-model"}},
				config: providers.Config{Connections: map[string]providers.Connection{
					"deepseek": {Model: "old-model", Storage: "file"},
				}},
			}
			for _, step := range []int{providerChoose, providerKey, providerModel} {
				m.providers.step = step

				area := m.providerArea()
				if area.x <= 0 || area.y <= 0 || area.width > 72 || area.y+area.height >= size[1] {
					t.Fatalf("dialog must fit with centered margins at %dx%d", size[0], size[1])
				}

				view := ansi.Strip(m.View().Content)
				if strings.Contains(view, "private-key") || !strings.Contains(view, m.homePath) {
					t.Fatal("dialogs must mask keys and retain the folder footer behind them")
				}

				if len(strings.Split(view, "\n")) != size[1] {
					t.Fatal("dialog view must preserve terminal height")
				}

				for _, line := range strings.Split(view, "\n") {
					if ansi.StringWidth(line) > size[0] {
						t.Fatal("dialog view exceeds terminal width")
					}
				}
			}
		}
	}
}

func TestProviderCheckingCanBeCanceledWithoutRestoringSecrets(t *testing.T) {
	m := dashboardFixture()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	m.providers = providerSettings{open: true, busy: true, step: providerKey, sequence: 5,
		key: textField{value: "private-key", limit: 4096}, cancel: cancel}
	next, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})

	m = next.(model)
	if ctx.Err() == nil || m.providers.busy || m.providers.key.value != "" ||
		m.providers.step != providerChoose {
		t.Fatal("Esc must cancel checking and clear the API key")
	}

	next, _ = m.Update(providerModelsLoaded{sequence: 5, secret: "late-secret",
		models: []providers.Model{{ID: "late-model"}}})

	m = next.(model)
	if m.providers.secret != "" || len(m.providers.models) != 0 || m.providers.step != providerChoose {
		t.Fatal("canceled provider requests must not reopen dialogs or restore credentials")
	}
}
