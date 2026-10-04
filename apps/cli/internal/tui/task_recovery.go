package tui

import (
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/rajandhamala/Maruvo/cli/internal/api"
)

func (m model) canRecover(post api.Post) bool {
	return m.isPoster(post) && post.AcceptedBy != nil &&
		(post.Status == "negotiating" || post.Status == "cancelled") &&
		m.escrow.State != "confirmed" && m.escrow.State != "released" && m.escrow.State != "refunded"
}

func (m model) beginRecovery(key string) (tea.Model, tea.Cmd) {
	post := m.posts[m.selected]
	if !m.canRecover(post) || (key == "x" && post.Status == "cancelled") {
		return m, nil
	}

	m.recovering, m.err, m.notice = "cancel", nil, ""
	if key == "o" {
		m.recovering = "reopen"
		if post.ReopenedAs != nil {
			m.loading = true
			return m, m.recoverPost(post)
		}

		m.recoveryDeadline = post.EndTime
		if !m.recoveryDeadline.After(time.Now()) {
			m.recoveryDeadline = time.Now().Add(24 * time.Hour).Truncate(time.Minute)
		}

		m.recoveryDelivery = time.Time{}
		if post.DeliverBy != nil {
			m.recoveryDelivery = *post.DeliverBy
		}
	}

	return m, nil
}

func (m model) recoveryLayout() postLayout {
	post := m.posts[m.selected]
	width := m.contentWidth()

	title, action := "Cancel this unfunded task?", "Cancel task"
	if m.recovering == "reopen" {
		title, action = "Reopen under a new task ID?", "Reopen as new"
	}

	l := postLayout{rows: []string{
		bold(title),
		muted(ansi.Truncate(plain(post.Title), width, "…")),
		muted(ansi.Truncate("Existing chat/files stay with the old task.", width, "…")),
	}}

	if m.recovering == "reopen" {
		date := "Accept by  " + m.recoveryDeadline.Local().Format("02 Jan 2006, 15:04") + " ▾"
		l.hit(0, len(l.rows), min(width, ansi.StringWidth(date)), 1, "recovery-deadline", 0)
		l.rows = append(l.rows, accent(ansi.Truncate(date, width, "…")))

		if post.DeliverBy != nil {
			if m.bodyHeight() <= 6 {
				l.rows = append(l.rows[:1], l.rows[2:]...)
				l.hits[0].y--
			}

			delivery := "Deliver by " + m.recoveryDelivery.Local().Format("02 Jan 2006, 15:04") + " ▾"
			l.hit(0, len(l.rows), min(width, ansi.StringWidth(delivery)), 1, "recovery-delivery", 0)
			l.rows = append(l.rows, accent(ansi.Truncate(delivery, width, "…")))
		}
	}

	if m.escrow.State == "prepared" || m.escrow.State == "pending" || m.escrow.State == "failed" {
		l.rows = append(l.rows, muted("Funding must expire before recovery."))
	}

	l.wrappedButtons(width, []string{action, "Back"}, []string{"recovery-confirm", "cancel"})

	l.footer = "Enter / y confirm · Esc back"
	if m.recovering == "reopen" {
		l.footer = "d change acceptance cutoff · Enter confirm · Esc back"
		if post.DeliverBy != nil {
			l.footer = "d accept date · t delivery date · Enter confirm · Esc back"
		}
	}

	return l
}
