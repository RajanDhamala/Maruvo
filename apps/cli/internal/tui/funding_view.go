package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/x/ansi"
)

func (m model) fundingLayout() postLayout {
	post := m.posts[m.selected]
	width, inner := m.contentWidth(), m.contentWidth()-6
	reviewTerms := "The authorized reviewer releases payment or approves a refund."
	if m.escrow.Reviewer == post.PosterWallet {
		reviewTerms = "You review delivery and sign payment release or a refund."
	}
	network := plain(m.escrow.Network)
	if network == "devnet" {
		network = "Devnet · test SOL"
	}
	build := func(compact bool) postLayout {
		l := postLayout{footer: "Enter / y approve funding · Esc cancel"}
		add := func(text string) {
			l.rows = append(l.rows, strings.Split(ansi.Wrap(text, inner, " "), "\n")...)
		}
		field := func(label, value string) {
			cells := 12
			if compact {
				cells = 10
			}
			add(muted(fmt.Sprintf("%-*s", max(cells, ansi.StringWidth(label)+1), label)) + value)
		}
		date := func(value time.Time) string {
			if compact {
				return value.Local().Format("02 Jan, 15:04")
			}
			return value.Local().Format("02 Jan 2006, 15:04 MST")
		}
		if compact {
			add(bold(fmt.Sprintf("Sign funding? · Task #%d", post.ID)))
		} else {
			add(bold("Fund this task's escrow?"))
			add(muted(plain(post.Title)))
			add("")
		}
		field("Payment", accent(taskBudget(post.CostLamports)))
		feeLabel := "Network fee"
		if compact {
			feeLabel = "Fee"
		}
		field(feeLabel, fmt.Sprintf("%d lamports", m.escrow.FeeLamports))
		field("Storage", fmt.Sprintf("%d lamports", m.escrow.StorageLamports))
		if compact {
			add(plain(m.escrow.Network) + " · Reviewer " + shortWallet(m.escrow.Reviewer))
		} else {
			field("Network", network)
			field("Reviewer", plain(m.escrow.Reviewer))
			add("")
		}
		if post.FundBy != nil {
			field("Fund by", date(*post.FundBy))
		} else if post.FundingWindowSeconds > 0 {
			field("Fund within", timingHours(post.FundingWindowSeconds)+" after acceptance")
		}
		if post.DeliverBy != nil {
			field("Deliver by", date(*post.DeliverBy))
		}
		if post.ReviewWindowSeconds > 0 {
			if compact {
				add("Review " + timingHours(post.ReviewWindowSeconds) + " after each delivery")
			} else {
				field("Review", timingHours(post.ReviewWindowSeconds)+" after each delivery")
			}
		}
		if compact {
			add(muted("Signing locks payment in escrow."))
		} else {
			add("")
			add(muted("Your signature locks this payment in escrow so work can start."))
			add(muted(reviewTerms))
			add("")
		}
		l.buttons([]string{"Sign & fund  Enter", "Cancel  Esc"}, []string{"fund-confirm", "cancel"})
		row := len(l.rows) - 1
		shift := max(0, (inner-ansi.StringWidth(l.rows[row]))/2)
		l.rows[row] = strings.Repeat(" ", shift) + l.rows[row]
		for i := range l.hits {
			l.hits[i].x += shift + 3
		}
		return l
	}
	l := build(false)
	if len(l.rows)+2 > m.bodyHeight() {
		l = build(true)
	}
	bordered := len(l.rows)+2 <= m.bodyHeight()
	rows := []string{}
	if bordered {
		rows = append(rows, detailSurface(muted("╭"+strings.Repeat("─", width-2)+"╮"), width))
	}
	for _, line := range l.rows {
		padding := strings.Repeat(" ", max(0, inner-ansi.StringWidth(line)))
		text := "   " + line + padding + "   "
		if bordered {
			text = muted("│  ") + line + padding + muted("  │")
		}
		rows = append(rows, detailSurface(text, width))
	}
	if bordered {
		rows = append(rows, detailSurface(muted("╰"+strings.Repeat("─", width-2)+"╯"), width))
	}
	top := max(0, (m.bodyHeight()-len(rows))/2)
	l.rows = append(make([]string, top), rows...)
	for i := range l.hits {
		l.hits[i].y += top
		if bordered {
			l.hits[i].y++
		}
	}
	return l
}
