package tui

import (
	"fmt"
	"image/color"
	"slices"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/rajandhamala/Maruvo/cli/internal/api"
)

const (
	dashboardTasks = iota
	dashboardPrompt
	dashboardSearch
)

type dashboardState struct {
	feed, mine    []api.Post
	ready         bool
	generation    uint64
	focus         int
	search        textField
	all           bool
	sidebarHidden bool
	promptHidden  bool
	filterOpen    bool
	filterChoice  int
}

type dashboardGeometry struct {
	mainWidth, sideX, sideWidth  int
	promptY, statusY, listHeight int
}

func (m model) dashboardGeometry() dashboardGeometry {
	width, height := m.contentWidth(), m.bodyHeight()

	g := dashboardGeometry{mainWidth: width, promptY: -1, statusY: height - 4}
	if !m.dashboard.promptHidden {
		g.promptY = height - homePromptHeight - 3
		g.statusY = g.promptY - 1
	}

	g.listHeight = g.statusY
	if !m.dashboard.sidebarHidden && width >= 108 && height >= 22 {
		g.sideWidth = min(40, max(32, width/4))
		g.mainWidth = width - g.sideWidth - 3
		g.sideX = g.mainWidth + 3
	}

	return g
}

func (m model) onDashboard() bool {
	return m.screen == feedScreen || m.screen == myPostsScreen
}

func (m model) dashboardView() tea.View {
	width, height := m.dimensions()
	if width < 48 || height < 16 {
		return m.frame(nil, "")
	}

	l := m.dashboardLayout()
	g := m.dashboardGeometry()

	label := muted(m.profileLabel())
	if m.profileOpen {
		label = accent(m.profileLabel())
	}

	brand := accent("MARUVO") + muted("  task exchange")

	header := align(brand, label, m.contentWidth())
	if g.sideWidth > 0 {
		header = brand
	}

	lines := []string{"", strings.Repeat(" ", m.contentX()) + header, ""}
	if g.sideWidth > 0 {
		for y := range lines {
			text := ""
			if y == 1 {
				text = strings.Repeat(" ", max(0, g.sideWidth-2-ansi.StringWidth(label))) + label
			}

			drawOverlay(width, lines, hitArea{x: m.contentX() + g.sideX, y: y, width: g.sideWidth},
				[]string{dashboardPanelRow(text, g.sideWidth)})
		}
	}

	for _, row := range l.rows {
		lines = append(lines, strings.Repeat(" ", m.contentX())+row)
	}

	if m.profileOpen {
		m.drawProfile(lines)
	}

	view := tea.NewView(strings.Join(lines, "\n"))
	view.AltScreen = true
	view.MouseMode = tea.MouseModeCellMotion
	view.BackgroundColor = color.RGBA{R: 10, G: 10, B: 10, A: 255}
	view.ForegroundColor = color.RGBA{R: 238, G: 238, B: 238, A: 255}

	return view
}

func (m model) dashboardSummary() string {
	if !m.dashboard.ready {
		return muted("Loading task overview…")
	}

	active, completed := 0, 0

	for _, post := range m.dashboard.mine {
		if activeTask(post) {
			active++
		}

		if post.Status == "completed" {
			completed++
		}
	}

	return muted(fmt.Sprintf("%d active · %d completed", active, completed))
}

func (m model) dashboardLayout() postLayout {
	width, height := m.contentWidth(), m.bodyHeight()
	l := postLayout{rows: make([]string, height)}
	g := m.dashboardGeometry()
	m.dashboardList(&l, g.mainWidth, g.listHeight)

	status := ""

	switch {
	case m.err != nil:
		status = warning(plain(m.err.Error()))
	case m.notice != "":
		status = accent(plain(m.notice))
	case !m.user.GitHubConnected && m.user.GitHubLogin == "":
		status = accent("Connect GitHub to your profile: Ctrl+g")
	}

	l.rows[g.statusY] = ansi.Truncate(status, g.mainWidth, "…")
	if g.promptY >= 0 {
		active := m.dashboard.focus == dashboardPrompt && !m.loading && !m.profileOpen
		submit := button(" Enter create ", active)
		rows := homePromptRows(m.homeInput, g.mainWidth, active, "Task", submit)
		drawOverlay(width, l.rows, hitArea{y: g.promptY, width: g.mainWidth}, rows)
		l.hit(
			g.mainWidth-ansi.StringWidth(submit)-1,
			g.promptY+3,
			ansi.StringWidth(submit),
			1,
			"dashboard-submit",
			0,
		)
		l.hit(0, g.promptY, g.mainWidth, homePromptHeight, "dashboard-prompt", 0)
	}

	help := "↑↓ browse · Enter open · / search · f filter · Tab prompt"
	controls := "n new · 1/2 view · r refresh · Ctrl+b sidebar · Ctrl+t prompt"

	if g.mainWidth < 76 {
		help = "↑↓ · Enter · / search · f filter · Tab input"
		controls = "1/2 view · Ctrl+b panel · Ctrl+t input"
	}

	if m.dashboard.focus == dashboardPrompt {
		help = "Describe a task · Enter create · Esc browse · Ctrl+c quit"
		if g.mainWidth < 76 {
			help = "Enter create · Esc browse · Ctrl+c quit"
		}
	} else if m.dashboard.focus == dashboardSearch {
		help = "Search title, person, or status · Enter browse · Esc back"
		if g.mainWidth < 76 {
			help = "Enter browse · Esc back · Ctrl+u clear"
		}
	}

	if m.profileOpen {
		help = "w wallet · Enter log out · Esc close"
	} else if m.dashboard.filterOpen {
		help = "↑↓ choose · Enter apply · Esc close"
	} else if m.dashboard.focus == dashboardTasks && len(m.listIndices()) == 0 && !m.loading {
		help = "Enter · / search · f filter · Tab prompt"
	}

	l.rows[height-3] = muted(ansi.Truncate(help, g.mainWidth, "…"))

	l.rows[height-2] = muted(ansi.Truncate(controls, g.mainWidth, "…"))
	if g.sideWidth > 0 {
		m.dashboardSidebar(&l, g.sideX, g.sideWidth, height)
	} else {
		path := m.homePath
		if path == "" {
			path = currentHomePath()
		}

		l.rows[height-1] = align(
			muted(ansi.Truncate(plain(path), max(1, width-23), "…")),
			muted("Ctrl+p account"),
			width,
		)
	}

	if m.dashboard.filterOpen && !m.profileOpen {
		m.drawDashboardFilter(&l)
	}

	return l
}

func (m model) dashboardList(l *postLayout, width, height int) {
	indices := m.listIndices()
	title, right := m.dashboardToolbar(l, width)
	l.rows[0] = title + strings.Repeat(
		" ",
		max(2, width-ansi.StringWidth(title)-ansi.StringWidth(right)),
	) + right
	y := 2

	if m.dashboard.focus == dashboardSearch || m.dashboard.search.value != "" {
		searchY := 1
		if height >= 6 {
			searchY = 2
			y = searchY + 2
		}

		text := m.dashboard.search.render(width-4, m.dashboard.focus == dashboardSearch)
		if m.dashboard.search.value == "" {
			text = "\x1b[7mS\x1b[0m" + muted("earch tasks…")
		}

		l.rows[searchY] = muted("\uf002  ") + text
		l.hit(0, searchY, width, 1, "dashboard-search", 0)
	}

	if len(indices) == 0 {
		m.dashboardEmpty(l, width, y, height)
		return
	}

	rowHeight := 3
	if height-y < 3 {
		rowHeight = max(1, height-y)
	}

	visible := max(1, (height-y)/rowHeight)
	position := max(0, slices.Index(indices, m.selected))
	start := max(0, position-visible+1)

	end := min(len(indices), start+visible)
	for _, i := range indices[start:end] {
		post := m.posts[i]

		marker, title := "  ", bold(plain(post.Title))
		if i == m.selected {
			marker, title = accent("› "), accent(plain(post.Title))
		}

		l.rows[y] = marker + align(title, bold(taskBudget(post.CostLamports)), width-2)
		if rowHeight >= 2 {
			meta := post.Level + " · " + participantLabel(post.Poster, post.UserID)
			if m.own {
				meta = taskRoles[m.taskRole(post)] + " · " + taskStatus(post) + " · " + post.Level
			}

			date := ""
			if !post.EndTime.IsZero() {
				date = "Accept by " + post.EndTime.Local().Format("02 Jan")
			}

			l.rows[y+1] = "  " + align(muted(meta), muted(date), width-2)
		}

		l.hit(0, y, width, min(2, rowHeight), "post", i)
		y += rowHeight
	}

	if start > 0 || end < len(indices) {
		if width >= 64 {
			label := dashboardChoice("Feed", !m.own) + "  " + dashboardChoice("Personal", m.own) +
				muted(fmt.Sprintf("  %d–%d / %d", start+1, end, len(indices)))
			label = ansi.Truncate(label, width-ansi.StringWidth(right)-2, "…")
			l.rows[0] = label + strings.Repeat(
				" ",
				width-ansi.StringWidth(label)-ansi.StringWidth(right),
			) + right
		}
	}
}

func (m model) dashboardSidebar(l *postLayout, x, width, height int) {
	for y := 0; y < height; y++ {
		drawOverlay(
			m.contentWidth(),
			l.rows,
			hitArea{x: x, y: y, width: width},
			[]string{dashboardPanelRow("", width)},
		)
	}

	put := func(y int, text string) {
		drawOverlay(m.contentWidth(), l.rows, hitArea{x: x, y: y, width: width},
			[]string{dashboardPanelRow("  "+ansi.Truncate(text, width-4, "…"), width)})
	}
	put(0, bold("Your wallet"))

	if m.walletAddress == "" {
		put(1, muted("Not connected"))
		l.hit(x+2, 1, width-4, 1, "dashboard-wallet", 0)
	} else {
		put(1, accent(shortWallet(m.walletAddress)))
	}

	put(3, m.dashboardSummary())
	put(5, bold("Active work"))

	y, active := 6, 0
	for i, post := range m.dashboard.mine {
		if !activeTask(post) || active >= 2 {
			continue
		}

		put(y, plain(post.Title))
		put(y+1, muted(taskStatus(post)+" · "+taskBudget(post.CostLamports)))
		l.hit(x+2, y, width-4, 2, "dashboard-task", i)
		y, active = y+3, active+1
	}

	if active == 0 {
		put(y, muted("Nothing active yet."))
		y += 2
	}

	path := m.homePath
	if path == "" {
		path = currentHomePath()
	}

	put(height-2, muted(plain(path)))

	if y+3 >= height-4 {
		return
	}

	put(y, bold("Recent task updates"))

	y++
	if len(m.dashboard.mine) == 0 {
		put(y, muted("Your task updates appear here."))
	}

	for i, post := range m.dashboard.mine {
		if y+1 >= height-4 {
			break
		}

		put(y, plain(post.Title))
		put(y+1, muted(taskStatus(post)+" · "+taskAge(postTime(post))))
		l.hit(x+2, y, width-4, 2, "dashboard-task", i)
		y += 3
	}
}

func dashboardPanelRow(text string, width int) string {
	background := "\x1b[48;2;18;18;18m"

	return background + strings.ReplaceAll(text, "\x1b[0m", "\x1b[0m"+background) +
		strings.Repeat(" ", max(0, width-ansi.StringWidth(text))) + "\x1b[0m"
}

func dashboardChoice(text string, active bool) string {
	if active {
		return "\x1b[1;4;36m" + text + "\x1b[0m"
	}

	return muted(text)
}

func activeTask(post api.Post) bool {
	return post.Status == "negotiating" || post.Status == "in_progress"
}

func taskStatus(post api.Post) string {
	if post.Status == "negotiating" {
		return "Awaiting funding"
	}

	return plain(strings.ReplaceAll(post.Status, "_", " "))
}

func shortWallet(address string) string {
	if len(address) <= 16 {
		return address
	}

	return address[:6] + "…" + address[len(address)-6:]
}

func postTime(post api.Post) time.Time {
	if post.UpdatedAt.IsZero() {
		return post.CreatedAt
	}

	return post.UpdatedAt
}

func taskAge(updated time.Time) string {
	if updated.IsZero() {
		return ""
	}

	age := time.Since(updated)
	if age < time.Minute {
		return "just now"
	}

	if age < time.Hour {
		return fmt.Sprintf("%dm ago", int(age.Minutes()))
	}

	if age < 24*time.Hour {
		return fmt.Sprintf("%dh ago", int(age.Hours()))
	}

	return updated.Local().Format("02 Jan")
}
