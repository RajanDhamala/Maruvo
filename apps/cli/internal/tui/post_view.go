package tui

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/rajandhamala/Maruvo/cli/internal/api"
)

func (m model) postsView() tea.View {
	if m.onDashboard() {
		return m.dashboardView()
	}

	if m.screen == newPostScreen {
		return m.formView()
	}

	l := m.postsLayout()

	return m.frame(l.rows, l.footer)
}

func (m model) postsLayout() postLayout {
	if m.screen == workspaceScreen {
		return m.workspaceLayout()
	}

	if m.screen == newPostScreen {
		return m.formLayout()
	}

	if m.screen == detailScreen && len(m.posts) != 0 {
		return m.detailLayout()
	}

	return m.listLayout()
}

func (m model) listLayout() postLayout {
	return m.dashboardLayout()
}

func (m model) detailLayout() postLayout {
	post := m.posts[m.selected]
	_, height := m.dimensions()

	l := postLayout{rows: m.detailRows(), footer: "↑↓ / wheel scroll   Esc back   n new   q quit"}
	if m.recovering != "" {
		l = m.recoveryLayout()
	} else if m.fundingConfirm {
		l = m.fundingLayout()
	} else if m.deleting {
		l.rows = []string{
			warning("Delete this post?"),
			"",
			plain(post.Title),
			"",
			muted("This action cannot be undone."),
			"",
		}
		if height < 20 {
			l.rows = []string{
				warning("Delete this post?"),
				plain(post.Title),
				"",
				muted("This action cannot be undone."),
				"",
			}
		}

		l.buttons([]string{"Delete post", "Cancel"}, []string{"delete", "cancel"})
		l.footer = "y delete   n / Esc cancel"
	} else if m.editingStatus {
		l.rows = []string{bold("Change post status"), muted(plain(post.Title)), ""}
		if height < 20 {
			l.rows = nil
		}

		for i, status := range statuses {
			label := "  " + strings.ReplaceAll(status, "_", " ")
			if i == m.statusChoice {
				label = accent("› " + strings.ReplaceAll(status, "_", " "))
			}

			l.hit(0, len(l.rows), m.contentWidth(), 1, "status", i)
			l.rows = append(l.rows, label)
		}

		if height >= 20 {
			l.rows = append(l.rows, "")
		}

		l.buttons([]string{"Save status", "Cancel"}, []string{"save-status", "cancel"})
		l.footer = "Click a status   Enter save   Esc cancel"
	} else {
		l = m.detailHeader()
		available := max(0, m.bodyHeight()-len(l.rows))
		rows := m.detailRows()
		start := min(m.scroll, max(0, len(rows)-available))
		l.rows = append(l.rows, rows[start:min(len(rows), start+available)]...)
	}

	if m.loading {
		l.footer = "Saving changes..."
	}

	return l
}

func (m model) detailHeader() postLayout {
	post := m.posts[m.selected]
	width := m.contentWidth()

	status := taskStatus(post)
	if post.Deadline.DueAt != nil && !time.Now().Before(*post.Deadline.DueAt) && activeTask(post) {
		status = deadlineLabel(post.Deadline)
	}

	l := postLayout{
		rows: []string{
			align(bold(plain(post.Title)), muted(fmt.Sprintf("#%d", post.ID)), width),
			align(
				muted(status+" · "+post.Level),
				bold(taskBudget(post.CostLamports)),
				width,
			),
		},
		footer: "↑↓ / wheel scroll · Esc back · n new · q quit",
	}
	if m.bodyHeight() >= 18 {
		role := ""
		if m.isPoster(post) {
			role = " · REQUESTER"
		} else if m.isWorker(post) {
			role = " · WORKER"
		}
		statusStyle := muted("● " + status)
		if post.Status == "negotiating" {
			statusStyle = "\x1b[38;2;230;190;110m● " + status + "\x1b[0m"
		}
		if strings.Contains(status, "overdue") {
			statusStyle = warning("● " + status)
		}
		l.rows = []string{
			detailSurface("  "+align(muted(fmt.Sprintf("TASK #%d%s", post.ID, role)), muted("BUDGET"), width-4), width),
			detailSurface("  "+align(bold(plain(post.Title)), accent(taskBudget(post.CostLamports)), width-4), width),
			detailSurface("  "+statusStyle+muted("   ·   "+post.Level), width),
		}
	} else if strings.Contains(status, "overdue") {
		l.rows[len(l.rows)-1] = warning(status + " · " + post.Level)
	}
	if m.bodyHeight() >= 12 {
		l.rows = append(l.rows, detailProgress(m.taskFlow(post, m.escrow, "", api.Settlement{}, false), width))
	}
	if m.bodyHeight() >= 18 {
		l.rows = append(l.rows, "")
	}
	buttons := func(labels, actions []string) {
		if width >= 72 {
			for i, action := range actions {
				if len(action) == 1 {
					labels[i] += "  " + action
				} else if action == "back" {
					labels[i] += "  esc"
				}
			}
		}
		l.wrappedButtons(width, labels, actions)
	}
	if m.isPoster(post) && post.AcceptedBy == nil {
		buttons([]string{"Change status", "Delete task", "Back"}, []string{"s", "d", "back"})
		l.footer = "s status · d delete · ↑↓ scroll · Esc back"
	} else if !m.isPoster(post) && post.Status == "open" && post.EndTime.After(time.Now()) &&
		(post.TargetWorker == nil || strconv.FormatInt(*post.TargetWorker, 10) == m.user.ID) {
		buttons([]string{"Accept task", "Back"}, []string{"a", "back"})
		l.footer = "a accept · w wallet · r refresh · ↑↓ scroll · Esc back"
	} else if m.isPoster(post) && post.AcceptedBy != nil && post.Status == "negotiating" && m.escrow.State != "confirmed" {
		labels, actions := []string{"Fund escrow", "Open workspace", "Share inputs"}, []string{"f", "c", "u"}
		if width < 64 {
			labels, actions = []string{"Fund escrow", "Workspace"}, []string{"f", "c"}
		}
		if m.escrow.State == "pending" {
			labels[0], actions[0] = "Refresh funding", "refresh"
		}
		if m.bodyHeight() < 18 {
			labels, actions = append(labels, "Back"), append(actions, "back")
		}

		buttons(labels, actions)
		if m.bodyHeight() >= 18 {
			secondaryLabels, secondaryActions := []string{"Esc Back"}, []string{"back"}
			if m.canRecover(post) {
				secondaryLabels = append([]string{"x Cancel task", "o Reopen as new"}, secondaryLabels...)
				secondaryActions = append([]string{"x", "o"}, secondaryActions...)
			}
			x, row := 0, ""
			for i, label := range secondaryLabels {
				if x > 0 && x+ansi.StringWidth(label) > width {
					l.rows = append(l.rows, row)
					x, row = 0, ""
				}
				l.hit(x, len(l.rows), ansi.StringWidth(label), 1, secondaryActions[i], 0)
				row += muted(label) + "   "
				x += ansi.StringWidth(label) + 3
			}
			l.rows = append(l.rows, row)
		}
		l.footer = "f fund · x cancel · o reopen · c workspace · r refresh · Esc back"
		if m.escrow.State == "pending" {
			l.footer = "r check funding · c workspace · Esc back"
		}
	} else if m.canRecover(post) && post.Status == "cancelled" {
		label := "Reopen as new"
		if post.ReopenedAs != nil {
			label = "View reopened task"
		}

		buttons([]string{label, "Chat", "Back"}, []string{"o", "c", "back"})
		l.footer = "o reopen · c history · r refresh · Esc back"
	} else if post.AcceptedBy != nil {
		buttons(
			[]string{"Open workspace", "Back"},
			[]string{"c", "back"},
		)
		l.footer = "c workspace · u file · r refresh · ↑↓ scroll · Esc back"
	} else {
		buttons([]string{"Refresh", "Back"}, []string{"refresh", "back"})
	}

	if width < 64 {
		l.footer = "↑↓ scroll · Esc back · r refresh · q quit"
	}
	if m.detailScrollMaxForHeader(len(l.rows)+1) > 0 {
		l.footer += " · PgUp/PgDn scroll · Home/End jump"
	}

	l.rows = append(l.rows, "")

	return l
}

func (m model) detailScrollMax() int {
	return m.detailScrollMaxForHeader(len(m.detailHeader().rows))
}

func (m model) detailScrollMaxForHeader(headerRows int) int {
	return max(0, len(m.detailRows())-max(0, m.bodyHeight()-headerRows))
}

func (m model) detailRows() []string {
	post := m.posts[m.selected]
	width := m.contentWidth()

	flow := m.taskFlow(post, m.escrow, "", api.Settlement{}, false)
	rows := []string{}
	if m.bodyHeight() < 12 {
		rows = append(rows, accent(flow.stage))
	}
	for _, line := range strings.Split(ansi.Wrap("Next: "+flow.next, width-2, " "), "\n") {
		rows = append(rows, accent("│ ")+muted(line))
	}
	rows = append(rows, "")
	rows = append(rows, m.detailSummary(post)...)

	for _, section := range []struct{ label, text string }{
		{"Description", post.Description},
		{"Expected result", post.AcceptanceCriteria},
		{"Input files", strings.Join(post.InputFiles, "\n")},
		{"Expected outputs", strings.Join(post.ExpectedOutputs, "\n")},
	} {
		if section.text == "" {
			continue
		}

		rows = append(rows, "", accent(section.label)+muted("  "+strings.Repeat("─", max(0, width-ansi.StringWidth(section.label)-2))), "")
		for _, line := range strings.Split(ansi.Strip(section.text), "\n") {
			rows = append(rows, strings.Split(ansi.Wrap(plain(line), min(width, 88), " "), "\n")...)
		}
	}
	profiles := []string{}
	for _, person := range []*api.PublicUser{post.Poster, post.Worker} {
		if person != nil && person.GitHubURL != "" {
			profiles = append(profiles, person.GitHubURL)
		}
	}
	if len(profiles) != 0 {
		rows = append(rows, "", muted("Participant profiles"))
		for _, profile := range profiles {
			rows = append(rows, strings.Split(ansi.Wrap(muted(plain(profile)), width, " "), "\n")...)
		}
	}
	if m.escrow.Address != "" {
		rows = append(rows, "", muted("Escrow address"))
		rows = append(rows, strings.Split(ansi.Wrap(plain(m.escrow.Address), width, " "), "\n")...)
	}

	return rows
}

func detailProgress(flow taskFlow, width int) string {
	step := 0
	for i := 1; i <= 4; i++ {
		if strings.HasPrefix(flow.stage, fmt.Sprintf("Step %d/4", i)) {
			step = i
		}
	}
	if step == 0 || width < 72 {
		return accent(flow.stage)
	}
	stages := []string{"Accept", "Fund escrow", "Deliver", "Review & pay"}
	for i, label := range stages {
		switch {
		case i+1 < step:
			stages[i] = muted("✓ " + label)
		case i+1 == step:
			label = strings.TrimPrefix(flow.stage, fmt.Sprintf("Step %d/4 · ", step))
			stages[i] = "\x1b[1;36;48;2;18;44;48m " + fmt.Sprintf("%02d", i+1) + " " + label + " \x1b[0m"
		default:
			stages[i] = muted(fmt.Sprintf("%02d %s", i+1, label))
		}
	}
	progress := strings.Join(stages, muted("  ─  "))
	if ansi.StringWidth(progress) > width {
		return accent(flow.stage)
	}
	return progress
}

func (m model) detailSummary(post api.Post) []string {
	people := []string{muted("Requester  ") + participantLabel(post.Poster, post.UserID)}
	if post.AcceptedBy != nil {
		people = append(people, muted("Worker     ")+participantLabel(post.Worker, *post.AcceptedBy))
	} else {
		people = append(people, muted("Worker     Awaiting acceptance"))
	}
	state := map[string]string{"": "Awaiting funding", "unfunded": "Awaiting funding", "prepared": "Ready to fund", "pending": "Funding pending", "confirmed": "Funded", "released": "Payment released", "refunded": "Refunded", "failed": "Funding failed", "expired": "Funding expired"}[m.escrow.State]
	if state == "" {
		state = plain(m.escrow.State)
	}
	people = append(people, muted("Escrow     ")+state)
	if m.escrow.Network != "" {
		network := plain(m.escrow.Network)
		if network == "devnet" {
			network = "Devnet · test SOL"
		}
		people = append(people, muted(network))
	}
	if post.ReopenedAs != nil {
		people = append(people, muted("Reopened   ")+fmt.Sprintf("#%d", *post.ReopenedAs))
	}
	formatDate := func(date time.Time) string { return date.Local().Format("02 Jan 2006, 15:04") }
	deadlines := []string{}
	if post.AcceptedBy == nil {
		deadlines = append(deadlines, muted("Accept by   ")+formatDate(post.EndTime))
	} else if post.AcceptedAt != nil {
		deadlines = append(deadlines, muted("Accepted    ")+formatDate(*post.AcceptedAt))
	}
	if post.FundBy != nil {
		deadlines = append(deadlines, muted("Fund by     ")+formatDate(*post.FundBy))
	} else if post.FundingWindowSeconds > 0 {
		deadlines = append(deadlines, muted("Fund within ")+timingHours(post.FundingWindowSeconds)+" of acceptance")
	}
	if post.DeliverBy != nil {
		deadlines = append(deadlines, muted("Deliver by  ")+formatDate(*post.DeliverBy))
	}
	if post.Deadline.Stage == "review" && post.Deadline.DueAt != nil {
		deadlines = append(deadlines, muted("Review by   ")+formatDate(*post.Deadline.DueAt))
	}
	if post.ReviewWindowSeconds > 0 {
		deadlines = append(deadlines, muted("Review      ")+timingHours(post.ReviewWindowSeconds)+" after each delivery")
	}
	width := m.contentWidth()
	panel := func(title string, content []string, cells int) []string {
		rows := []string{bold(title)}
		for _, line := range content {
			rows = append(rows, strings.Split(ansi.Wrap(line, cells, " "), "\n")...)
		}
		return rows
	}
	card := func(rows []string, cells, height int) []string {
		top := muted("╭─ ") + rows[0] + muted(" "+strings.Repeat("─", max(0, cells-ansi.StringWidth(rows[0])-5))+"╮")
		result := []string{detailSurface(top, cells)}
		for i := 1; i < height; i++ {
			line := ""
			if i < len(rows) {
				line = rows[i]
			}
			result = append(result, detailSurface(muted("│ ")+line+strings.Repeat(" ", max(0, cells-4-ansi.StringWidth(line)))+muted(" │"), cells))
		}
		return append(result, detailSurface(muted("╰"+strings.Repeat("─", cells-2)+"╯"), cells))
	}
	roomy := m.bodyHeight() >= 24
	if width < 72 {
		cells := width
		if roomy {
			cells -= 4
		}
		left, right := panel("Participants & payment", people, cells), panel("Deadlines · local time", deadlines, cells)
		if roomy {
			left, right = card(left, width, len(left)), card(right, width, len(right))
		}
		return append(append(left, ""), right...)
	}
	cells := (width - 5) / 2
	inner := cells
	if roomy {
		inner -= 4
	}
	left, right := panel("Participants & payment", people, inner), panel("Deadlines · local time", deadlines, inner)
	gap := muted("  │  ")
	if roomy {
		height := max(len(left), len(right))
		left, right = card(left, cells, height), card(right, cells, height)
		gap = "     "
	}
	rows := []string{}
	for i := 0; i < max(len(left), len(right)); i++ {
		a, b := "", ""
		if i < len(left) {
			a = left[i]
		}
		if i < len(right) {
			b = right[i]
		}
		rows = append(rows, a+strings.Repeat(" ", max(0, cells-ansi.StringWidth(a)))+gap+b)
	}
	return rows
}

func detailSurface(text string, width int) string {
	background := "\x1b[48;2;18;22;26m"
	return background + strings.ReplaceAll(text, "\x1b[0m", "\x1b[0m"+background) +
		strings.Repeat(" ", max(0, width-ansi.StringWidth(text))) + "\x1b[0m"
}
