package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/x/ansi"
)

func (m model) reviewHeader() []string {
	width, state, post := m.contentWidth(), m.workspace.State, m.workspace.Post
	title := fmt.Sprintf("REVIEW DELIVERY · #%d", post.ID)
	version := fmt.Sprintf("Version %d", state.SubmissionVersion)
	status := strings.ReplaceAll(state.ReviewState, "_", " ") + " · Escrow " + m.workspace.Escrow.State
	if m.workspace.Escrow.Network != "" {
		status += " · " + m.workspace.Escrow.Network
	}
	if m.bodyHeight() < 18 {
		return []string{
			align(bold(fmt.Sprintf("Delivery v%d · #%d", state.SubmissionVersion, post.ID)), muted(state.ReviewState), width),
			muted(ansi.Truncate(plain(post.Title), width, "…")),
		}
	}
	return []string{
		detailSurface("  "+align(muted(title), accent(version), width-4), width),
		detailSurface("  "+align(bold(plain(post.Title)), accent(taskBudget(post.CostLamports)), width-4), width),
		detailSurface("  "+muted(status), width),
		"",
	}
}

func (m model) reviewButtons() postLayout {
	l := postLayout{footer: "↑↓ scroll · PgUp/PgDn · Tab files · Esc chat"}
	switch {
	case m.canReviewAction("a"):
		labels, actions := []string{"Approve & pay  a", "Request changes  e"}, []string{"a", "e"}
		if m.canReviewAction("x") {
			labels, actions = append(labels, "Refund  x"), append(actions, "x")
		}
		l.wrappedButtons(m.contentWidth(), append(labels, "Files  Tab"), append(actions, "workspace-tab"))
	case m.canReviewAction("x"):
		l.wrappedButtons(m.contentWidth(), []string{"Refund  x", "Chat  Esc", "Files  Tab"},
			[]string{"x", "v", "workspace-tab"})
	default:
		l.wrappedButtons(m.contentWidth(), []string{"Chat  Esc", "Files  Tab"}, []string{"v", "workspace-tab"})
	}
	if m.workspace.State.RefundBlocked && m.workspace.Escrow.State == "confirmed" {
		for _, row := range reviewTextRows("File access recorded · Refund blocked in Maruvo", m.contentWidth()) {
			l.rows = append(l.rows, muted(row))
		}
	}
	for i := range l.rows {
		l.rows[i] = strings.TrimRight(l.rows[i], " ")
	}
	return l
}

func (m model) reviewBodyHeight() int {
	return max(1, m.bodyHeight()-len(m.reviewHeader())-len(m.reviewButtons().rows)-1)
}

func (m model) reviewMaxScroll() int {
	return max(0, len(m.reviewContent().rows)-m.reviewBodyHeight())
}

func (m model) reviewLayout() postLayout {
	l := postLayout{rows: m.reviewHeader()}
	body, controls := m.reviewContent(), m.reviewButtons()
	available := m.reviewBodyHeight()
	start := min(max(0, m.activityScroll), max(0, len(body.rows)-available))
	end, offset := min(len(body.rows), start+available), len(l.rows)
	l.rows = append(l.rows, body.rows[start:end]...)
	for _, hit := range body.hits {
		if hit.y >= start && hit.y < end {
			hit.y += offset - start
			l.hits = append(l.hits, hit)
		}
	}
	for len(l.rows) < offset+available {
		l.rows = append(l.rows, "")
	}
	position := ""
	if len(body.rows) > available {
		position = fmt.Sprintf("%d–%d / %d", start+1, end, len(body.rows))
	}
	l.rows = append(l.rows, align(muted(strings.Repeat("─", min(20, m.contentWidth()/3))), muted(position), m.contentWidth()))
	for _, hit := range controls.hits {
		hit.y += len(l.rows)
		l.hits = append(l.hits, hit)
	}
	l.rows = append(l.rows, controls.rows...)
	l.footer = controls.footer
	return l
}

func (m model) reviewContent() postLayout {
	l := postLayout{}
	state, width := m.workspace.State, m.contentWidth()
	textWidth := min(88, width)
	if state.SubmittedAt == nil {
		l.rows = append(l.rows, muted("The worker has not submitted delivery yet."))
	} else {
		l.rows = append(l.rows, muted("Submitted  "+state.SubmittedAt.Local().Format("02 Jan 2006, 15:04")))
		if state.ReviewBy != nil {
			label := "Review by  " + state.ReviewBy.Local().Format("02 Jan 2006, 15:04")
			if !time.Now().Before(*state.ReviewBy) {
				label += " · overdue"
			}
			l.rows = append(l.rows, accent(label))
		}
		if m.workspace.Post.DeliverBy != nil && state.SubmittedAt.After(*m.workspace.Post.DeliverBy) {
			l.rows = append(l.rows, muted("Delivered after the agreed deadline."))
		}
	}
	if m.workspace.Settlement.State != "" {
		l.rows = append(l.rows, muted("Settlement: "+plain(m.workspace.Settlement.Action)+" · "+plain(m.workspace.Settlement.State)))
	}
	if m.workspace.Escrow.State == "confirmed" {
		text := "Downloading worker outputs records access and blocks unilateral refunds in Maruvo."
		if state.RefundBlocked {
			text = "Worker file access is recorded. Refunds through Maruvo are blocked pending manual dispute resolution; no automated resolver is available."
		}
		for _, row := range reviewTextRows(text, textWidth) {
			l.rows = append(l.rows, muted(row))
		}
	}

	l.rows = append(l.rows, "", accent(fmt.Sprintf("Submitted files · %d", len(state.DeliveryFiles))))
	if len(state.DeliveryFiles) == 0 {
		l.rows = append(l.rows, muted("No files attached to this delivery."))
	} else {
		l.rows = append(l.rows, muted(fmt.Sprintf("delivery-v%d/", state.SubmissionVersion)))
		archive := false
		for i, id := range state.DeliveryFiles {
			branch, indent := "├── ", "│   "
			if i == len(state.DeliveryFiles)-1 {
				branch, indent = "└── ", "    "
			}
			index := -1
			for j, file := range m.workspace.Files {
				if file.ID == id {
					index = j
					break
				}
			}
			if index < 0 {
				l.rows = append(l.rows, muted(branch+"Unavailable file"), muted(indent+ansi.Truncate(plain(id), width-4, "…")))
				continue
			}
			file := m.workspace.Files[index]
			name := plain(file.Name)
			download := "↓ Download"
			label := ansi.Truncate(branch+name, width-ansi.StringWidth(download)-2, "…")
			l.hit(0, len(l.rows), width, 1, "workspace-download", index)
			l.rows = append(l.rows, label+strings.Repeat(" ", max(2, width-ansi.StringWidth(label)-ansi.StringWidth(download)))+accent(download))
			kind := "File"
			switch {
			case strings.HasSuffix(name, ".tar.gz"), strings.HasSuffix(name, ".tgz"), strings.HasSuffix(name, ".zip"):
				kind, archive = "Archive", true
			case strings.HasSuffix(name, ".md"):
				kind = "Markdown"
			case strings.HasSuffix(name, ".patch"), strings.HasSuffix(name, ".diff"):
				kind = "Patch"
			}
			metadata := kind + " · " + fileSize(file.Size)
			if file.SHA256 != "" {
				metadata += " · SHA256 " + ansi.Truncate(plain(file.SHA256), 12, "…")
			}
			l.rows = append(l.rows, muted(indent+metadata))
		}
		if archive {
			l.rows = append(l.rows, "")
			for _, row := range reviewTextRows("Download archives to inspect their bundled source files.", textWidth) {
				l.rows = append(l.rows, muted(row))
			}
		}
	}
	if state.Submission != "" {
		l.rows = append(l.rows, "", accent("Worker report"), muted("Delivery notes and worker-reported tests"), "")
		l.rows = append(l.rows, reviewTextRows(state.Submission, textWidth)...)
	}
	if state.ReviewNote != "" {
		l.rows = append(l.rows, "", accent("Requested changes"), "")
		l.rows = append(l.rows, reviewTextRows(state.ReviewNote, textWidth)...)
	}
	return l
}

func reviewTextRows(text string, width int) []string {
	rows := []string{}
	code := false
	for _, line := range strings.Split(strings.ReplaceAll(ansi.Strip(text), "\r\n", "\n"), "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "```") {
			code = !code
			continue
		}
		if code {
			line = strings.ReplaceAll(line, "\t", "    ")
		} else {
			line = plain(line)
		}
		heading := false
		if !code && strings.HasPrefix(line, "#") {
			label := strings.TrimLeft(line, "#")
			if len(line)-len(label) <= 6 && strings.HasPrefix(label, " ") {
				line, heading = strings.TrimSpace(label), true
			}
		}
		for _, row := range strings.Split(ansi.Wrap(line, max(1, width), " "), "\n") {
			switch {
			case code:
				row = muted(row)
			case heading:
				row = bold(row)
			}
			rows = append(rows, row)
		}
	}
	return rows
}
