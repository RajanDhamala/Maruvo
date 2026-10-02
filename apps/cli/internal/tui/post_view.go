package tui

import (
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

func (m model) postsView() tea.View {
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
	width := m.contentWidth()
	_, height := m.dimensions()
	l := postLayout{footer: "Click / Enter open   ↑↓ browse   n new   r refresh   l logout   q quit"}

	title := "Find your next task"
	if m.own {
		title = "Your posts"
	}

	refresh := button(" Refresh ", false)
	refreshWidth := ansi.StringWidth(refresh)
	heading := func(count string) string {
		return align(bold(title), muted(count), width-refreshWidth-3) + "   " + refresh
	}
	l.rows = []string{heading(fmt.Sprintf("%d posts", len(m.posts)))}
	l.hit(width-refreshWidth, 0, refreshWidth, 1, "refresh", 0)

	if m.own {
		l.rows = append(l.rows, muted("Posts you created, accepted, or review.  w connect wallet"))
	} else {
		line, x := "", 0

		for i, level := range levels {
			text := " " + level + " "
			line += button(text, i == m.level) + "  "
			l.hit(x, 1, ansi.StringWidth(text), 1, "feed-level", i)
			x += ansi.StringWidth(text) + 2
		}

		l.rows = append(l.rows, line)
		l.footer = "Click / Enter open   ↑↓ browse   ←→ level   n new   r refresh   q quit"
	}

	l.rows = append(l.rows, "")
	if m.loading {
		l.rows = append(l.rows, muted("Loading posts..."))
		return l
	}

	if len(m.posts) == 0 {
		message := "No posts at this level yet. Try another level."
		if m.own {
			message = "No posts yet. Create your first one."
		}

		l.rows = append(l.rows, muted(message), "")
		l.buttons([]string{"New post"}, []string{"new"})

		return l
	}

	visible := max(1, (height-13)/5)
	start := max(0, m.selected-visible+1)

	end := min(len(m.posts), start+visible)
	for i := start; i < end; i++ {
		post := m.posts[i]

		bar, title := muted("│ "), bold(plain(post.Title))
		if i == m.selected {
			bar, title = accent("┃ "), accent(plain(post.Title))
		}

		y := len(l.rows)
		l.rows = append(
			l.rows,
			bar+align(title, bold(fmt.Sprintf("%d lamports", post.CostLamports)), width-2),
			bar+muted(post.Level+"  ·  "+strings.ReplaceAll(post.Status, "_", " ")),
			bar+align(
				muted("Due "+post.EndTime.Local().Format("02 Jan 2006, 15:04")),
				muted("Open →"),
				width-2,
			),
			muted(strings.Repeat("─", width)),
			"",
		)
		l.hit(0, y, width, 3, "post", i)
	}

	if start > 0 || end < len(m.posts) {
		l.rows[0] = heading(fmt.Sprintf("%d–%d of %d", start+1, end, len(m.posts)))
	}

	return l
}

func (m model) detailLayout() postLayout {
	post := m.posts[m.selected]
	_, height := m.dimensions()

	l := postLayout{rows: m.detailRows(), footer: "↑↓ / wheel scroll   Esc back   n new   q quit"}
	if m.fundingConfirm {
		l.rows = []string{bold("Fund this task's escrow?"), muted(plain(post.Title)), "",
			fmt.Sprintf("Payment       %d lamports", post.CostLamports),
			fmt.Sprintf("Network fee   %d lamports", m.escrow.FeeLamports),
			fmt.Sprintf("Storage       %d lamports", m.escrow.StorageLamports),
			"Network       " + m.escrow.Network,
			"Reviewer      " + ansi.Truncate(m.escrow.Reviewer, max(1, m.contentWidth()-14), "…"),
			muted("Funds stay in escrow until the reviewer settles."), ""}
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
		if m.isPoster(post) && post.AcceptedBy == nil {
			l.buttons([]string{"Change status", "Delete post", "Back"}, []string{"s", "d", "back"})
			l.footer = "s status   d delete   ↑↓ / wheel scroll   Esc back   q quit"
		} else if !m.isPoster(post) && post.Status == "open" && post.EndTime.After(time.Now()) {
			l.buttons([]string{"Accept task", "Back"}, []string{"a", "back"})
			l.footer = "a accept   w connect wallet   r refresh   Esc back"
		} else if m.isPoster(post) && post.AcceptedBy != nil && post.Status == "negotiating" {
			l.buttons([]string{"Fund escrow", "Chat", "Send file", "Back"}, []string{"f", "c", "u", "back"})
			l.footer = "f fund escrow   c chat   m message   u send file   r refresh   Esc back"
		} else if post.AcceptedBy != nil {
			l.buttons([]string{"Chat", "Send file", "Refresh", "Back"}, []string{"c", "u", "refresh", "back"})
			l.footer = "c chat   m message   u send file   r refresh   Esc back   q quit"
		} else {
			l.buttons([]string{"Refresh", "Back"}, []string{"refresh", "back"})
			l.footer = "r refresh funding   Esc back   q quit"
		}

		start := min(m.scroll, max(0, len(l.rows)-(height-10)))

		l.rows = l.rows[start:]
		for i := range l.hits {
			l.hits[i].y -= start
		}
	}

	if m.loading {
		l.footer = "Saving changes..."
	}

	return l
}

func (m model) detailRows() []string {
	post := m.posts[m.selected]
	rows := strings.Split(ansi.Wrap(bold(plain(post.Title)), max(1, m.contentWidth()), " "), "\n")

	rows = append(rows, "", muted(fmt.Sprintf("Post #%d", post.ID)), "",
		"Level      "+post.Level,
		"Status     "+strings.ReplaceAll(post.Status, "_", " "),
		fmt.Sprintf("Cost       %d lamports", post.CostLamports),
		"Accept by  "+post.EndTime.Local().Format("02 Jan 2006, 15:04 MST"), "")
	if post.AcceptedBy != nil {
		rows = append(rows, fmt.Sprintf("Worker     User #%d", *post.AcceptedBy))

		state := m.escrow.State
		if state == "" {
			state = "unfunded"
		}

		rows = append(rows, "Escrow     "+state)
		if state == "unfunded" || state == "prepared" || state == "pending" || state == "expired" ||
			state == "failed" {
			rows = append(rows, muted("Waiting for confirmed funding. Work has not started."))
		}

		if m.escrow.Address != "" {
			rows = append(
				rows,
				"Network    "+m.escrow.Network,
				"Escrow     "+ansi.Truncate(m.escrow.Address, max(1, m.contentWidth()-11), "…"),
			)
		}

		rows = append(rows, "")
	}

	for _, section := range []struct{ label, text string }{{"Instructions", post.Description}, {"Acceptance criteria", post.AcceptanceCriteria}, {"Input files", strings.Join(post.InputFiles, ", ")}, {"Expected outputs", strings.Join(post.ExpectedOutputs, ", ")}} {
		if section.text != "" {
			rows = append(rows, "", bold(section.label))
			rows = append(
				rows,
				strings.Split(ansi.Wrap(plain(section.text), max(1, m.contentWidth()), " "), "\n")...)
		}
	}

	return rows
}
