package tui

import (
	"context"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/rajandhamala/Maruvo/cli/internal/providers"
)

func TestHelpPreservesInputsAndConsumesActions(t *testing.T) {
	m := dashboardFixture()
	m.homeInput = textField{value: "Keep my draft", cursor: 4}
	m.dashboard.focus = dashboardPrompt
	next, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyF1})

	m = next.(model)
	if cmd != nil || !m.shortcutsOpen {
		t.Fatal("F1 should open help locally")
	}

	for _, msg := range []tea.Msg{tea.PasteMsg{Content: "overwrite"}, tea.KeyPressMsg{Code: tea.KeyEnter}, tea.KeyPressMsg{Code: 'n', Text: "n"}} {
		next, cmd = m.Update(msg)

		m = next.(model)
		if cmd != nil || m.homeInput.value != "Keep my draft" || m.homeInput.cursor != 4 ||
			m.screen != feedScreen {
			t.Fatal("help must not edit or submit the underlying draft")
		}
	}

	next, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})

	m = next.(model)
	if m.shortcutsOpen || m.dashboard.focus != dashboardPrompt {
		t.Fatal("closing help must restore input focus")
	}

	next, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyF1})

	next, _ = next.Update(tea.MouseClickMsg{Button: tea.MouseLeft, X: 5, Y: 5})
	if next.(model).shortcutsOpen || next.(model).homeInput.value != "Keep my draft" {
		t.Fatal("click should dismiss help without activating underlying controls")
	}
}

func TestHelpKeepsSecretsMaskedAndApprovalsPending(t *testing.T) {
	m := dashboardFixture()
	m.providers = providerSettings{
		open: true,
		step: providerKey,
		key:  textField{value: "secret-api-key", cursor: 4},
	}
	next, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyF1})

	m = next.(model)
	if strings.Contains(m.View().Content, "secret-api-key") {
		t.Fatal("help exposed provider key")
	}

	next, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyF1})

	m = next.(model)
	if m.providers.key.value != "secret-api-key" || m.providers.key.cursor != 4 {
		t.Fatal("help changed the provider draft")
	}

	m.providers.open = false

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	answer := make(chan bool, 1)
	m.localAgent = localAgentState{
		open:     true,
		busy:     true,
		ctx:      ctx,
		cancel:   cancel,
		approval: &providers.Approval{Path: "output.txt", Content: "Keep content"},
		answer:   answer,
	}
	next, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyF1})
	next, cmd := next.Update(tea.KeyPressMsg{Code: 'y', Text: "y"})

	m = next.(model)
	if cmd != nil || m.localAgent.approval == nil || len(answer) != 0 || ctx.Err() != nil {
		t.Fatal("help must not approve or cancel pending file edits")
	}

	next, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	if next.(model).localAgent.approval == nil || ctx.Err() != nil {
		t.Fatal("Esc should close help first")
	}
}

func TestHelpNeverSignsPendingPayment(t *testing.T) {
	m := reviewerModel()
	m.token = "session"
	m.reviewConfirm = true
	m.shortcutsOpen = true
	next, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})

	m = next.(model)
	if cmd != nil || !m.reviewConfirm || !m.shortcutsOpen {
		t.Fatal("Enter in help must not sign settlement")
	}

	next, cmd = m.Update(tea.KeyPressMsg{Code: 'y', Text: "y"})
	if cmd != nil || !next.(model).reviewConfirm {
		t.Fatal("y in help must not sign settlement")
	}
}

func TestCompactHintsAndHelpFitEveryScreen(t *testing.T) {
	for _, size := range [][2]int{{48, 16}, {80, 24}, {120, 36}, {240, 60}} {
		for _, screen := range []string{"dashboard", "prompt", "search", "home", "form", "timings", "calendar", "detail", "workspace", "review", "provider", "key", "models", "settings", "agent", "approval", "commands", "directory"} {
			m := dashboardFixture()
			m.width, m.height = size[0], size[1]

			switch screen {
			case "prompt":
				m.dashboard.focus = dashboardPrompt
			case "search":
				m.dashboard.focus = dashboardSearch
			case "home":
				m.token = ""
			case "form", "timings":
				m.screen, m.form = newPostScreen, newPostForm()
				m.form.timingOpen = screen == "timings"
			case "calendar":
				m.screen, m.form = newPostScreen, newPostForm()
				m = m.openDeadline()
			case "detail":
				m.screen = detailScreen
			case "workspace", "review":
				m.screen = workspaceScreen
				m.workspace.Post = m.posts[0]
				m.workspaceReview = screen == "review"
			case "provider", "key", "models", "settings":
				m.providers = providerSettings{open: true}
				if screen == "key" {
					m.providers.step = providerKey
				}

				if screen == "models" {
					m.providers.step = providerModel
				}

				if screen == "settings" {
					m.providers.step = providerOptions
				}
			case "agent", "approval":
				m.localAgent = localAgentState{open: true}
				if screen == "approval" {
					m.localAgent.approval = &providers.Approval{Path: "output.txt", Content: "change"}
				}
			case "commands", "directory":
				m = m.openCommands()
				m.commands.directory = screen == "directory"
			}

			for _, help := range []bool{false, true} {
				m.shortcutsOpen = help
				view := m.View()

				lines := strings.Split(view.Content, "\n")
				if len(lines) != size[1] {
					t.Fatalf("%v %s help=%v: row count %d", size, screen, help, len(lines))
				}

				for _, row := range lines {
					if ansi.StringWidth(row) > size[0] {
						t.Fatalf("%v %s: overflowing row", size, screen)
					}
				}

				if help && view.MouseMode != tea.MouseModeCellMotion {
					t.Fatal("help must receive mouse scrolling and dismissal")
				}

				if help && !strings.Contains(view.Content, "Keyboard shortcuts") {
					t.Fatalf("%s: missing help", screen)
				}

				if !help && !strings.Contains(view.Content, "F1 help") {
					t.Fatalf("%v %s: help is undiscoverable", size, screen)
				}
			}
		}
	}

	for _, hint := range []string{"Enter / y approve funding   Esc cancel", "Enter / y sign decision   Esc cancel", "y apply edit · n decline"} {
		compact := compactHint(hint, 44)
		if strings.Contains(compact, "…") || ansi.StringWidth(compact) > 44 {
			t.Fatalf("approval instructions truncated: %s", compact)
		}
	}
}

func TestHelpScrollsWithoutLosingHiddenShortcuts(t *testing.T) {
	m := dashboardFixture()

	m.width, m.height, m.shortcutsOpen = 48, 16, true
	if !strings.Contains(strings.Join(m.shortcutRows(), "\n"), "r refresh") {
		t.Fatal("small terminals must retain complete help")
	}

	for i := 0; i < 50; i++ {
		next, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyDown})
		m = next.(model)
	}

	if !strings.Contains(m.View().Content, "Ctrl+c quit") {
		t.Fatal("scroll must reach the last shortcut")
	}

	before := m.shortcutScroll

	next, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyUp})
	if next.(model).shortcutScroll != before-1 {
		t.Fatal("help scroll must stop at its content boundary")
	}
}
