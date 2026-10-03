package tui

import (
	"errors"
	"image/color"
	"os"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

const (
	homePrompt = iota
	homeGitHub
	homeGoogle
)

const homePromptHeight = 5

var homeWordmark = []string{
	" ███▄  ▄███     ███▄     ██▀▀██▄   ██   ███  ███   ▄██   ▄▄█▀▀██▄ ",
	" ████▄▄████    ██▀██     ██  ███   ██   ███   ██▄  ██    ██    ██▄",
	" ██ ███▀ ██   ▄██ ▀██    ██▀▀██▄   ██   ███    ██ ██▀    ██    ███",
	" ██  ▀▀  ██  ▄██▀▀▀██▄   ██  ▀██   ███  ██▀    ▀████     ██▄  ▄██ ",
	" ▀▀      ▀▀  ▀▀▀    ▀▀   ▀▀   ▀▀▀   ▀▀▀▀▀       ▀▀▀        ▀▀▀▀▀  ",
}

var homeWordmarkCompact = []string{
	" ███  ███    ███▄    ██▀██▄   ██  ██  ▀█▄  ██▀  ▄█▀▀█▄  ",
	" ███▄████   ▄█▀▀█    ██ ▄█▀   ██  ██   ██ ▄█▀  ▄██   ██ ",
	" ██ ██ ██   █▀▀▀██   ██▀██▄   ██  ██    ████    ██  ▄█▀ ",
	" ▀▀    ▀▀  ▀▀    ▀▀  ▀▀  ▀▀    ▀▀▀▀     ▀▀▀      ▀▀▀▀   ",
}

func currentHomePath() string {
	path, _ := os.Getwd()

	home, _ := os.UserHomeDir()
	if home != "" && (path == home || strings.HasPrefix(path, home+string(os.PathSeparator))) {
		path = "~" + strings.TrimPrefix(path, home)
	}

	return path
}

func (m model) authView() tea.View {
	width, height := m.dimensions()
	if width < 48 || height < 16 {
		return m.frame(nil, "")
	}

	l := m.authLayout()
	view := tea.NewView(strings.Join(l.rows, "\n"))
	view.AltScreen = true
	view.MouseMode = tea.MouseModeCellMotion
	view.BackgroundColor = color.RGBA{R: 10, G: 10, B: 10, A: 255}
	view.ForegroundColor = color.RGBA{R: 238, G: 238, B: 238, A: 255}

	return view
}

func (m model) authLayout() postLayout {
	width, height := m.dimensions()
	l := postLayout{rows: make([]string, height)}
	paneWidth := min(104, width-4)
	x, inputY := (width-paneWidth)/2, height-homePromptHeight-4

	logo := []string{"M A R U V O"}
	if width >= 60 && height >= 24 {
		logo = homeWordmarkCompact
	}

	if width >= 72 && height >= 32 {
		logo = homeWordmark
	}

	buttonHeight := 3

	heroHeight := len(logo) + buttonHeight + 2
	if height >= 20 {
		heroHeight += 2
	}

	if height >= 30 {
		heroHeight += 2
	}

	y := max(0, (inputY-heroHeight)/2)
	for _, row := range logo {
		l.homeCenter(y, bold(row), width)
		y++
	}

	if height >= 30 {
		y++
	}

	subtitle := "Find work. Post a task."
	if m.loading {
		subtitle = "Checking your saved session…"
		if m.loggingIn {
			subtitle = "Continue in your browser."
		}
	}

	if height >= 20 {
		l.homeCenter(y, muted(subtitle), width)
		y += 2
	}

	buttonWidth := 29
	if width < 64 {
		buttonWidth = 23
	}

	buttonX := (width - 2*buttonWidth - 2) / 2

	for i, provider := range []string{"github", "google"} {
		label, icon := "Sign in with GitHub", "\uf09b"
		if provider == "google" {
			label, icon = "Sign in with Google", "\uf1a0"
		}

		rows := homeButton(label, icon, buttonWidth, buttonHeight,
			m.homeFocus == homeGitHub+i && !m.loading, m.loading)
		area := hitArea{x: buttonX + i*(buttonWidth+2), y: y, width: buttonWidth, height: buttonHeight}
		drawOverlay(width, l.rows, area, rows)
		l.hit(area.x, area.y, area.width, area.height, provider, 0)
	}

	y += buttonHeight
	if height >= 30 {
		y++
	}

	guidance := []string{
		"GitHub sign-in connects your profile.",
		"Existing Google user? Choose Google first.",
	}
	if m.loginProvider() == "google" {
		guidance = []string{
			"Already use Google? Sign in here first.",
			"Connect GitHub from your profile afterward.",
		}
	}

	if m.loading {
		guidance = []string{""}
		if m.loggingIn {
			guidance = []string{"Waiting for " + m.loginProvider() + " sign-in…"}
		}
	}

	for _, row := range guidance {
		l.homeCenter(y, muted(row), width)
		y++
	}

	if m.err != nil {
		message := ansi.Truncate(plain(m.err.Error()), paneWidth, "…")
		l.homeCenter(inputY-1, warning(message), width)
	}

	active := m.homeFocus == homePrompt && !m.loading

	rows := homePromptRows(m.homeInput, paneWidth, active, "Task · Sign in required", muted("Enter submit"))

	drawOverlay(width, l.rows, hitArea{x: x, y: inputY, width: paneWidth, height: homePromptHeight}, rows)
	l.hit(x, inputY, paneWidth, homePromptHeight, "prompt", 0)

	hint := "Tab select · Ctrl+g GitHub · Ctrl+o Google"
	if m.loading {
		hint = "Waiting for sign-in · Ctrl+c quit"
	}

	l.homeCenter(inputY+homePromptHeight, muted(hint), width)
	foot := align(muted(ansi.Truncate(plain(m.homePath), max(1, width-24), "…")),
		muted("Ctrl+c quit"), width-4)
	l.rows[height-2] = "  " + foot

	return l
}

func homePromptRows(field textField, width int, active bool, mode, submit string) []string {
	text := field.render(width-5, active)
	if field.value == "" {
		text = muted("Describe a task…")
		if active {
			text = "\x1b[7mD\x1b[0m" + muted("escribe a task…")
		}
	}

	meta := align(muted(mode), submit, width-4)

	rows := []string{
		inputRow("", width-1),
		inputRow(" "+text, width-1),
		inputRow("", width-1),
		inputRow(" "+meta, width-1),
		inputRow("", width-1),
	}
	for i, row := range rows {
		rail := muted("│")
		if active {
			rail = accent("▎")
		}

		rows[i] = rail + row
	}

	return rows
}

func (l *postLayout) homeCenter(y int, text string, width int) {
	text = ansi.Truncate(text, width-4, "…")
	l.rows[y] = strings.Repeat(" ", max(0, (width-ansi.StringWidth(text))/2)) + text
}

func homeButton(label, icon string, width, height int, active, disabled bool) []string {
	iconStyle := "\x1b[1;37m"
	if icon == "\uf1a0" {
		iconStyle = "\x1b[1;38;2;66;133;244m"
	}

	badge := iconStyle + icon + "\x1b[0m "
	if width >= 29 {
		badge = iconStyle + "\x1b[48;5;236m " + icon + " \x1b[0m  "
	}

	label = badge + label
	space := width - 2 - ansi.StringWidth(label)

	rows := make([]string, height)
	rows[0] = "╭" + strings.Repeat("─", width-2) + "╮"
	rows[height-1] = "╰" + strings.Repeat("─", width-2) + "╯"

	for i := 1; i < height-1; i++ {
		rows[i] = "│" + strings.Repeat(" ", width-2) + "│"
	}

	rows[height/2] = "│" + strings.Repeat(" ", space/2) + label + strings.Repeat(" ", space-space/2) + "│"
	for i, row := range rows {
		switch {
		case disabled:
			rows[i] = muted(ansi.Strip(row))
		case active:
			rows[i] = accent(strings.ReplaceAll(row, "\x1b[0m", "\x1b[0m\x1b[1;36m"))
		default:
			rows[i] = row
		}
	}

	return rows
}

func (m model) updateAuthMouse(msg tea.MouseClickMsg) (tea.Model, tea.Cmd) {
	for _, hit := range m.authLayout().hits {
		if msg.X < hit.x || msg.X >= hit.x+hit.width || msg.Y < hit.y || msg.Y >= hit.y+hit.height {
			continue
		}

		if hit.action != "prompt" {
			return m.beginSignIn(hit.action)
		}

		start := 0
		if m.homeFocus == homePrompt {
			start = m.homeInput.visibleStart(hit.width - 5)
		}

		m.homeFocus = homePrompt
		if msg.Y != hit.y+1 {
			return m, nil
		}

		m.homeInput.cursor = start
		runes := []rune(m.homeInput.value)
		column := max(0, msg.X-hit.x-3)

		for m.homeInput.cursor < len(runes) {
			cells := ansi.StringWidth(string(runes[m.homeInput.cursor]))
			if column < cells {
				break
			}

			column -= cells
			m.homeInput.cursor++
		}

		return m, nil
	}

	return m, nil
}

func (m model) updateAuth(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	if m.loading {
		if msg.String() == "esc" || msg.String() == "q" {
			return m, tea.Quit
		}

		return m, nil
	}

	switch msg.String() {
	case "tab":
		m.homeFocus = (m.homeFocus + 1) % 3
	case "shift+tab":
		m.homeFocus = (m.homeFocus + 2) % 3
	case "ctrl+g":
		return m.beginSignIn("github")
	case "ctrl+o":
		return m.beginSignIn("google")
	case "esc":
		m.homeFocus = homeGitHub
		if m.loginProvider() == "google" {
			m.homeFocus = homeGoogle
		}
	case "enter":
		if m.homeFocus == homeGitHub {
			return m.beginSignIn("github")
		}

		if m.homeFocus == homeGoogle {
			return m.beginSignIn("google")
		}

		m.err = errors.New("Sign in to continue. Choose GitHub or Google above.")
	default:
		if m.homeFocus == homePrompt || msg.Text != "" {
			m.homeFocus, m.homeInput.limit = homePrompt, 2000
			m.homeInput.key(msg)
		}
	}

	if m.homeFocus == homeGitHub {
		m.authProvider = "github"
	} else if m.homeFocus == homeGoogle {
		m.authProvider = "google"
	}

	return m, nil
}
