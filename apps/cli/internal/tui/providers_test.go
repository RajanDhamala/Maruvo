package tui

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/rajandhamala/Maruvo/cli/internal/providers"
	"github.com/zalando/go-keyring"
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

func TestStartupProviderSetup(t *testing.T) {
	for _, tc := range []struct {
		name   string
		config providers.Config
		err    error
		open   bool
	}{
		{name: "missing", open: true},
		{name: "connected", config: providers.Config{Connections: map[string]providers.Connection{
			"deepseek": {Model: "deepseek-flash", Storage: "encrypted"},
		}}},
		{name: "invalid config", err: errors.New("invalid local provider configuration"), open: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := dashboardFixture()
			m.dashboard.focus = dashboardPrompt
			next, _ := m.Update(startupProviderLoaded{config: tc.config, err: tc.err})
			m = next.(model)
			if m.providers.open != tc.open || m.providers.busy || m.providers.err != tc.err {
				t.Fatal("startup provider check did not preserve connection/error state")
			}
			if tc.open {
				next, _ = m.closeProviders()
				m = next.(model)
			}
			if m.dashboard.focus != dashboardPrompt {
				t.Fatal("provider setup lost prompt focus")
			}
		})
	}
}

func TestStartupProviderCheckCannotReopenDismissedSetup(t *testing.T) {
	m := dashboardFixture()
	next, _ := m.openProviders()
	m = next.(model)
	next, _ = m.closeProviders()
	m = next.(model)
	next, _ = m.Update(startupProviderLoaded{})
	if next.(model).providers.open {
		t.Fatal("stale startup check reopened provider setup")
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
		if !strings.Contains(view, "output.md") || !strings.Contains(view, "Yes") || !strings.Contains(view, "No") {
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

func TestDashboardAgentPromptLoading(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	m := dashboardFixture()
	m.homeInput = textField{value: "hello are u here?", cursor: 17, limit: 2000}
	m.dashboard.focus = dashboardPrompt
	next, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	next, followup := next.Update(cmd())

	m = next.(model)
	if followup != nil || m.localAgent.busy || m.localAgent.err == nil ||
		!strings.Contains(m.localAgent.err.Error(), "/model") || m.homeInput.value != "hello are u here?" {
		t.Fatal("missing provider must explain how to connect and preserve the prompt")
	}

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	m.ctx, m.directory = ctx, t.TempDir()

	client, err := providers.NewClient("deepseek", "test-model", "fake-key")
	if err != nil {
		t.Fatal(err)
	}

	sequence := m.localAgent.sequence

	next, cmd = m.Update(localAgentReady{sequence: sequence - 1, client: client})
	if cmd != nil || next.(model).localAgent.client != nil {
		t.Fatal("stale provider results must not submit a prompt")
	}

	next, cmd = m.Update(localAgentReady{sequence: sequence, client: client})

	m = next.(model)
	defer m.localAgent.cancel()

	if cmd == nil || !m.localAgent.busy || m.localAgent.autoSend || m.homeInput.value != "" ||
		!slices.Contains(m.localAgent.lines, "You: hello are u here?") || m.screen == newPostScreen {
		t.Fatal("ready provider must automatically submit the prompt once without opening creation")
	}

	next, cmd = m.Update(localAgentReady{sequence: sequence, client: client})
	if cmd != nil || len(next.(model).localAgent.lines) != len(m.localAgent.lines) {
		t.Fatal("duplicate readiness must not resend the prompt")
	}
}

func TestLocalAgentRefreshesMutatedPosts(t *testing.T) {
	for _, exit := range []bool{false, true} {
		m := dashboardFixture()
		m.localAgent = localAgentState{open: true, busy: true, sequence: 3, ctx: t.Context()}
		next, _ := m.Update(localAgentUpdate{sequence: 3, event: providers.Event{
			Type:   "tool",
			ToolID: "create",
			Status: "succeeded",
			Text:   "✓ Create post · Fix login\n  post #70",
		}})

		m = next.(model)
		if !m.localAgent.postsChanged {
			t.Fatal("successful post mutation must mark the dashboard for refresh")
		}

		var cmd tea.Cmd
		if exit {
			next, cmd = m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
		} else {
			next, cmd = m.Update(localAgentUpdate{sequence: 3, done: true})
		}

		m = next.(model)
		if cmd == nil || !m.loading || m.dashboard.generation != 4 || m.localAgent.postsChanged {
			t.Fatal("completed or interrupted post mutations must refresh the dashboard")
		}
	}
}

func TestDashboardEmptyPromptAndExplicitCreation(t *testing.T) {
	for _, text := range []string{"", "   "} {
		m := dashboardFixture()
		m.homeInput = textField{value: text}
		m.dashboard.focus = dashboardPrompt

		next, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
		if cmd != nil || next.(model).localAgent.open || !next.(model).onDashboard() {
			t.Fatal("empty prompts must not start the agent or open creation")
		}
	}

	m := dashboardFixture()
	m.homeInput = textField{value: "Explicit task draft", cursor: 19, limit: 2000}
	next, cmd := m.Update(tea.KeyPressMsg{Code: 'n', Text: "n"})

	m = next.(model)
	if cmd != nil || m.localAgent.open || m.screen != newPostScreen ||
		m.form.fields[3].value != "Explicit task draft" {
		t.Fatal("explicit new-task actions must retain manual creation")
	}
}

func TestLocalAgentReportedUsageIsVisible(t *testing.T) {
	m := dashboardFixture()
	m.localAgent = localAgentState{open: true, sequence: 3, ctx: context.Background()}
	next, _ := m.Update(localAgentUpdate{sequence: 3, event: providers.Event{
		Type: "usage", Text: "Usage (API): input 123 · output 45 · cache hit 100",
	}})

	m = next.(model)
	for _, size := range [][2]int{{48, 16}, {80, 24}, {120, 36}} {
		m.width, m.height = size[0], size[1]

		view := ansi.Strip(m.View().Content)

		text := strings.Join(strings.Fields(view), " ")
		if !strings.Contains(text, "Usage (API): input 123") || !strings.Contains(text, "output 45") ||
			(size[1] >= 22 && !strings.Contains(text, "cache hit 100")) ||
			(size[1] < 22 && !strings.Contains(text, "…")) ||
			strings.Contains(view, "Tool: Usage") {
			t.Fatalf("reported usage must be visible as usage at %dx%d", size[0], size[1])
		}

		for _, line := range strings.Split(view, "\n") {
			if ansi.StringWidth(line) > size[0] {
				t.Fatal("usage overflowed the terminal")
			}
		}
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
				chosen: providers.Model{
					ID:        "test-model",
					Reasoning: &providers.ReasoningSupport{SupportsMaxTokens: true},
				},
				config: providers.Config{Connections: map[string]providers.Connection{
					"deepseek": {Model: "old-model", Storage: "file"},
				}},
			}

			m.providers.provider = 1
			for _, step := range []int{providerChoose, providerKey, providerModel, providerOptions} {
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

func TestProviderSettingsSaveEncryptedAndPreserveDraft(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	keyring.MockInit()

	m := dashboardFixture()
	m.homeInput = textField{value: "Keep the draft", limit: 2000}
	m.providers = providerSettings{open: true, step: providerModel, sequence: 5,
		models: []providers.Model{{ID: "deepseek-flash"}}, secret: "fake-local-provider-key"}
	next, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})

	m = next.(model)
	if cmd != nil || m.providers.step != providerOptions {
		t.Fatal("model selection must open settings before saving")
	}

	for range 3 {
		next, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyRight})
		m = next.(model)
	}

	if m.providers.options.Reasoning != "high" {
		t.Fatal("DeepSeek levels must be default/off/low/high/max")
	}

	next, _ = m.Update(tea.PasteMsg{Content: "accidental-paste"})

	m = next.(model)
	if m.providers.query.value != "" {
		t.Fatal("settings must ignore unrelated pasted input")
	}

	next, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyTab})
	next, _ = next.Update(tea.KeyPressMsg{Code: tea.KeyRight})

	m = next.(model)
	if m.providers.options.MaxTokens != 32768 {
		t.Fatal("output control must update the limit")
	}

	next, cmd = m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})

	m = next.(model)
	if cmd == nil || !m.providers.saving || m.providers.secret != "" || m.providers.key.value != "" {
		t.Fatal("save must clear credentials from the view state")
	}

	next, _ = m.Update(cmd())

	m = next.(model)
	if m.providers.open || m.homeInput.value != "Keep the draft" {
		t.Fatal("save must close the modal and preserve the task draft")
	}

	key, connection, err := providers.Credential(m.profile, "deepseek")
	if err != nil || key != "fake-local-provider-key" || connection.Storage != "encrypted" ||
		connection.Reasoning != "high" || connection.MaxTokens != 32768 {
		t.Fatalf("saved settings/key: %+v, %v", connection, err)
	}
}

func TestProviderSettingsHonorMandatoryReasoningAndBudgets(t *testing.T) {
	m := dashboardFixture()

	m.providers = providerSettings{open: true, provider: 1, step: providerOptions,
		chosen: providers.Model{ID: "author/model", Reasoning: &providers.ReasoningSupport{
			SupportedEfforts: []byte(`["high","low","none"]`), Mandatory: true, SupportsMaxTokens: true,
		}}, options: providers.Options{MaxTokens: 8192}, secret: "fake-key"}
	if values := m.providerOptionValues(0); strings.Contains(strings.Join(values, ","), "none") {
		t.Fatal("mandatory reasoning must not offer Off")
	}

	next, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyRight})

	m = next.(model)
	if m.providers.options.Reasoning != "low" {
		t.Fatal("use only the model's advertised effort levels")
	}

	m.providers.optionRow = 2
	next, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyRight})

	m = next.(model)
	if m.providers.options.ReasoningTokens != 1024 || m.providers.options.Reasoning != "" {
		t.Fatal("reasoning budget must replace effort")
	}

	m.providers.optionRow = 0
	next, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyRight})

	m = next.(model)
	if m.providers.options.ReasoningTokens != 0 {
		t.Fatal("effort must replace reasoning budget")
	}

	next, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})

	m = next.(model)
	if m.providers.step != providerModel || m.providers.secret != "fake-key" {
		t.Fatal("Esc from settings must return to model selection")
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
