package tui

import (
	"errors"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

func TestHomeDraftRequiresSignIn(t *testing.T) {
	m := model{width: 80, height: 24}
	for _, r := range "go query" {
		updated, cmd := m.Update(tea.KeyPressMsg{Code: r, Text: string(r)})

		m = updated.(model)
		if cmd != nil || m.loading || m.loggingIn {
			t.Fatal("typing in the prompt must not run shortcuts or start authentication")
		}
	}

	updated, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyLeft})
	updated, _ = updated.Update(tea.KeyPressMsg{Code: tea.KeyBackspace})
	updated, _ = updated.Update(tea.PasteMsg{Content: "\x1b[31mन\x1b[0m\n"})

	m = updated.(model)
	if m.homeInput.value != "go queन y" {
		t.Fatalf("prompt editing or sanitized paste failed: %q", m.homeInput.value)
	}

	draft := m.homeInput.value
	updated, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})

	m = updated.(model)
	if cmd != nil || m.loading || m.loggingIn || m.homeInput.value != draft || m.err == nil {
		t.Fatal("submitting without a session must retain the draft and require sign-in")
	}

	if !strings.Contains(ansi.Strip(m.View().Content), "Sign in to continue.") {
		t.Fatal("the sign-in error must be visible beside the prompt")
	}

	updated, cmd = m.Update(tea.KeyPressMsg{Code: 'g', Mod: tea.ModCtrl})

	m = updated.(model)
	if cmd == nil || !m.loggingIn || m.err != nil || m.homeInput.value != draft {
		t.Fatal("sign-in should clear the error and retain the prompt draft")
	}

	updated, _ = m.Update(authResult{err: errors.New("Sign-in interrupted.")})

	m = updated.(model)
	if m.loading || m.loggingIn || m.homeInput.value != draft || m.token != "" {
		t.Fatal("failed sign-in must return to the editable home without losing the draft")
	}
}

func TestHomeFocusAndMouse(t *testing.T) {
	for _, size := range [][2]int{{48, 16}, {80, 24}, {168, 44}} {
		m := model{width: size[0], height: size[1]}

		updated, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyTab, Mod: tea.ModShift})
		if next := updated.(model); next.homeFocus != homeGoogle || next.loginProvider() != "google" {
			t.Fatal("reverse Tab must reach Google from the prompt")
		}

		updated, _ = updated.Update(tea.KeyPressMsg{Code: tea.KeyTab})
		if updated.(model).homeFocus != homePrompt {
			t.Fatal("Tab must cycle back to the prompt")
		}

		for _, hit := range m.authLayout().hits {
			for row := 0; row < hit.height; row++ {
				updated, cmd := m.Update(tea.MouseClickMsg{
					X: hit.x + hit.width/2, Y: hit.y + row, Button: tea.MouseLeft,
				})

				next := updated.(model)
				if hit.action == "prompt" {
					if cmd != nil || next.homeFocus != homePrompt || next.loggingIn {
						t.Fatal("clicking the prompt must focus it without starting authentication")
					}
				} else if cmd == nil || !next.loggingIn || next.loginProvider() != hit.action {
					t.Fatal("the whole sign-in button must activate its provider")
				}
			}
		}

		m.homeFocus = homeGoogle
		m.homeInput = textField{value: strings.Repeat("x", 200), cursor: 200, limit: 2000}

		for _, hit := range m.authLayout().hits {
			if hit.action != "prompt" {
				continue
			}

			updated, _ = m.Update(tea.MouseClickMsg{X: hit.x + 3, Y: hit.y + 1, Button: tea.MouseLeft})
			if updated.(model).homeInput.cursor != 0 {
				t.Fatal("clicking an unfocused prompt must place the caret in its displayed text")
			}
		}
	}
}

func TestHomeResponsiveStates(t *testing.T) {
	for _, size := range [][2]int{{48, 16}, {64, 20}, {80, 24}, {120, 36}, {168, 44}} {
		for _, state := range []string{"idle", "checking", "login", "error"} {
			m := model{width: size[0], height: size[1], homePath: "~/Desktop/aislop"}

			switch state {
			case "checking":
				m.loading = true
			case "login":
				m.loading, m.loggingIn = true, true
			case "error":
				m.err = errors.New("Sign in to continue.")
			}

			rows := strings.Split(m.View().Content, "\n")
			if len(rows) != size[1] {
				t.Fatalf("%v %s: home must fit the screen height", size, state)
			}

			for _, row := range rows {
				if ansi.StringWidth(row) > size[0] {
					t.Fatalf("%v %s: row exceeds screen width", size, state)
				}
			}

			view := ansi.Strip(m.View().Content)
			for _, text := range []string{"Sign in with GitHub", "Sign in with Google", "Describe a task…",
				"~/Desktop/aislop", "Ctrl+c quit"} {
				if !strings.Contains(view, text) {
					t.Fatalf("%v %s: missing %q", size, state, text)
				}
			}

			for _, hit := range m.authLayout().hits {
				if hit.x < 0 || hit.x+hit.width > size[0] || hit.y < 0 || hit.y+hit.height > size[1] {
					t.Fatalf("%v %s: interactive control exceeds the screen", size, state)
				}

				if hit.action == "prompt" && hit.y+hit.height < size[1]*3/4 {
					t.Fatal("the prompt must stay near the bottom")
				}
			}

			if m.loading {
				updated, cmd := m.Update(tea.PasteMsg{Content: "ignored while signing in"})
				if cmd != nil || updated.(model).homeInput.value != "" {
					t.Fatal("input must remain disabled while checking or signing in")
				}
			}
		}
	}
}
