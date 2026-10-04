package tui

import (
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
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
		l.rows = []string{bold("Fund this task's escrow?"), muted(plain(post.Title)), "",
			fmt.Sprintf("Payment       %d lamports", post.CostLamports),
			fmt.Sprintf("Network fee   %d lamports", m.escrow.FeeLamports),
			fmt.Sprintf("Storage       %d lamports", m.escrow.StorageLamports),
			"Network       " + m.escrow.Network,
			"Reviewer      " + ansi.Truncate(m.escrow.Reviewer, max(1, m.contentWidth()-14), "…"),
			muted("Funds stay in escrow until the reviewer settles."), ""}
		if post.DeliverBy != nil {
			l.rows = append(l.rows[:len(l.rows)-1], taskTimingRows(post)...)
		}

		if m.bodyHeight() < len(l.rows)+1 {
			l.rows = []string{
				bold("Sign funding?"),
				fmt.Sprintf(
					"Pay %d · Fee %d · Storage %d",
					post.CostLamports,
					m.escrow.FeeLamports,
					m.escrow.StorageLamports,
				),
				m.escrow.Network + " · Reviewer " + shortWallet(m.escrow.Reviewer),
			}
			if post.DeliverBy != nil && post.FundBy != nil {
				l.rows = append(
					l.rows,
					"Fund by "+post.FundBy.Local().Format("02 Jan, 15:04"),
					"Deliver "+post.DeliverBy.Local().
						Format("02 Jan, 15:04")+
						" · Review "+timingHours(
						post.ReviewWindowSeconds,
					),
				)
			}
		}

		l.buttons([]string{"Sign & fund", "Cancel"}, []string{"fund-confirm", "cancel"})
		l.footer = "Enter / y approve funding   Esc cancel"
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

	status := strings.ReplaceAll(post.Status, "_", " ")
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
	if m.isPoster(post) && post.AcceptedBy == nil {
		l.wrappedButtons(width, []string{"Change status", "Delete task", "Back"}, []string{"s", "d", "back"})
		l.footer = "s status · d delete · ↑↓ scroll · Esc back"
	} else if !m.isPoster(post) && post.Status == "open" && post.EndTime.After(time.Now()) {
		l.wrappedButtons(width, []string{"Accept task", "Back"}, []string{"a", "back"})
		l.footer = "a accept · w wallet · r refresh · ↑↓ scroll · Esc back"
	} else if m.isPoster(post) && post.AcceptedBy != nil && post.Status == "negotiating" {
		labels, actions := []string{"Fund escrow", "Chat", "Send file"}, []string{"f", "c", "u"}
		if m.canRecover(post) {
			labels, actions = append(labels, "Cancel task", "Reopen as new"), append(actions, "x", "o")
		}

		l.wrappedButtons(
			width,
			append(labels, "Back"),
			append(actions, "back"),
		)
		l.footer = "f fund · x cancel · o reopen · c chat · r refresh · Esc back"
	} else if m.canRecover(post) && post.Status == "cancelled" {
		label := "Reopen as new"
		if post.ReopenedAs != nil {
			label = "View reopened task"
		}

		l.wrappedButtons(width, []string{label, "Chat", "Back"}, []string{"o", "c", "back"})
		l.footer = "o reopen · c history · r refresh · Esc back"
	} else if post.AcceptedBy != nil {
		l.wrappedButtons(
			width,
			[]string{"Chat", "Send file", "Refresh", "Back"},
			[]string{"c", "u", "refresh", "back"},
		)
		l.footer = "c chat · u file · r refresh · ↑↓ scroll · Esc back"
	} else {
		l.wrappedButtons(width, []string{"Refresh", "Back"}, []string{"refresh", "back"})
	}

	if width < 64 {
		l.footer = "↑↓ scroll · Esc back · r refresh · q quit"
	}

	l.rows = append(l.rows, muted(strings.Repeat("─", width)))

	return l
}

func (m model) detailScrollMax() int {
	return max(0, len(m.detailRows())-max(0, m.bodyHeight()-len(m.detailHeader().rows)))
}

func (m model) detailRows() []string {
	post := m.posts[m.selected]
	width := m.contentWidth()

	rows := []string{
		"Posted by  " + participantLabel(post.Poster, post.UserID),
		"Accept by  " + post.EndTime.Local().Format("02 Jan 2006, 15:04 MST"),
		fmt.Sprintf("Budget     %d lamports", post.CostLamports),
	}
	for _, row := range taskTimingRows(post) {
		rows = append(rows, strings.Split(ansi.Wrap(row, width, " "), "\n")...)
	}

	if post.Poster != nil && post.Poster.GitHubURL != "" {
		rows = append(rows, "GitHub     "+post.Poster.GitHubURL)
	}

	if post.ReopenedAs != nil {
		rows = append(rows, fmt.Sprintf("Reopened   #%d", *post.ReopenedAs))
	}

	if post.AcceptedBy != nil {
		state := m.escrow.State
		if state == "" {
			state = "unfunded"
		}

		rows = append(
			rows,
			"Worker     "+participantLabel(post.Worker, *post.AcceptedBy),
			"Escrow     "+state,
		)
		if post.Worker != nil && post.Worker.GitHubURL != "" {
			rows = append(rows, "GitHub     "+post.Worker.GitHubURL)
		}

		if state == "unfunded" || state == "prepared" || state == "pending" || state == "expired" ||
			state == "failed" {
			rows = append(
				rows,
				strings.Split(
					ansi.Wrap(muted("Waiting for confirmed funding before work starts."), width, " "),
					"\n",
				)...)
		}

		if m.escrow.Address != "" {
			rows = append(rows, "Network    "+m.escrow.Network,
				"Address    "+ansi.Truncate(m.escrow.Address, max(1, width-11), "…"))
		}
	}

	for _, section := range []struct{ label, text string }{
		{"Description", post.Description},
		{"Expected result", post.AcceptanceCriteria},
		{"Input files", strings.Join(post.InputFiles, "\n")},
		{"Expected outputs", strings.Join(post.ExpectedOutputs, "\n")},
	} {
		if section.text == "" {
			continue
		}

		rows = append(rows, "", bold(section.label))
		for _, line := range strings.Split(ansi.Strip(section.text), "\n") {
			rows = append(rows, strings.Split(ansi.Wrap(plain(line), width, " "), "\n")...)
		}
	}

	return rows
}
