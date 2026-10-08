package tui

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/rajandhamala/Maruvo/cli/internal/api"
)

type deadlineClock time.Time

func deadlineTick() tea.Cmd {
	return tea.Tick(time.Minute, func(now time.Time) tea.Msg { return deadlineClock(now) })
}

func (f *postForm) syncDefaultDelivery() {
	if !f.deliveryDefault && f.timings[1].value != "" {
		return
	}

	acceptBy, err := time.ParseInLocation("2006-01-02 15:04", f.fields[2].value, time.Local)
	if err != nil {
		return
	}

	funding, err := strconv.ParseInt(strings.TrimSpace(f.timings[0].value), 10, 64)
	if err != nil || funding < 1 || funding > 720 {
		return
	}

	field := &f.timings[1]
	field.value = acceptBy.Add(time.Duration(funding+24) * time.Hour).Format("2006-01-02 15:04")
	field.cursor, f.deliveryDefault = len(field.value), true
}

func (f postForm) timingPayload(acceptBy time.Time) (int64, time.Time, int64, error) {
	funding, err := strconv.ParseInt(strings.TrimSpace(f.timings[0].value), 10, 64)
	if err != nil || funding < 1 || funding > 720 {
		return 0, time.Time{}, 0, errors.New("Funding window must be 1–720 hours. Open Timings with Ctrl+d.")
	}

	review, err := strconv.ParseInt(strings.TrimSpace(f.timings[2].value), 10, 64)
	if err != nil || review < 1 || review > 720 {
		return 0, time.Time{}, 0, errors.New("Review window must be 1–720 hours. Open Timings with Ctrl+d.")
	}

	delivery, err := time.ParseInLocation("2006-01-02 15:04", f.timings[1].value, time.Local)
	if err != nil || !delivery.After(acceptBy.Add(time.Duration(funding)*time.Hour)) {
		return 0, time.Time{}, 0, errors.New(
			"Choose Deliver by after Accept by plus the funding window. Open Timings with Ctrl+d.",
		)
	}

	return funding * 3600, delivery, review * 3600, nil
}

func (m model) updateTimings(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc", "ctrl+d":
		m.form.timingOpen = false
	case "tab", "down":
		m.form.timingFocus = (m.form.timingFocus + 1) % 4
	case "shift+tab", "up":
		m.form.timingFocus = (m.form.timingFocus + 3) % 4
	case "enter", "space", " ":
		if m.form.timingFocus == 1 {
			return m.openDeliveryDeadline(), nil
		}

		if m.form.timingFocus == 3 {
			m.form.timingOpen = false
		} else {
			m.form.timingFocus++
		}
	default:
		if m.form.timingFocus == 0 || m.form.timingFocus == 2 {
			m.form.timings[m.form.timingFocus].key(msg)

			if m.form.timingFocus == 0 {
				m.form.syncDefaultDelivery()
			}
		}
	}

	return m, nil
}

func (m model) timingLayout() postLayout {
	width := m.contentWidth()
	l := postLayout{
		rows:   []string{bold("Task timings · local time")},
		footer: "Tab next · Enter date / done · Esc back",
	}
	labels := []string{"Fund within hours of acceptance", "Deliver by", "Review within hours of delivery"}

	for i, field := range m.form.timings {
		value := field.render(max(1, width-35), m.form.timingFocus == i)
		if i == 1 {
			value = "Choose date ▾"
			if field.value != "" {
				value = field.value + " ▾"
			}
		}

		row := labels[i] + "  " + value
		if m.form.timingFocus == i {
			row = accent(row)
		}

		l.hit(0, len(l.rows), width, 1, "timing-field", i)
		l.rows = append(l.rows, ansi.Truncate(row, width, "…"))
	}

	l.rows = append(l.rows, muted("Refunds require reviewer approval."))
	l.buttons([]string{"Done"}, []string{"timing-done"})

	return l
}

func timingHours(seconds int64) string {
	if seconds%3600 == 0 {
		return fmt.Sprintf("%dh", seconds/3600)
	}

	return fmt.Sprintf("%dm", seconds/60)
}

func deadlineLabel(deadline api.TaskDeadline) string {
	if deadline.DueAt == nil || deadline.Stage == "" {
		return ""
	}

	label := map[string]string{"accept": "Accept", "fund": "Funding", "deliver": "Delivery", "review": "Review"}[deadline.Stage]
	if !time.Now().Before(*deadline.DueAt) {
		return label + " overdue"
	}

	return label + " by " + deadline.DueAt.Local().Format("02 Jan, 15:04")
}

func taskTimingRows(post api.Post) []string {
	if post.DeliverBy == nil {
		return nil
	}

	rows := []string{"Fund within  " + timingHours(post.FundingWindowSeconds) + " after acceptance",
		"Deliver by   " + post.DeliverBy.Local().Format("02 Jan 2006, 15:04 MST"),
		"Review within  " + timingHours(post.ReviewWindowSeconds) + " of each delivery"}
	if post.FundBy != nil {
		rows = append(rows, "Fund by      "+post.FundBy.Local().Format("02 Jan 2006, 15:04 MST"))
	}

	if label := deadlineLabel(post.Deadline); label != "" {
		if post.Deadline.DueAt != nil && !time.Now().Before(*post.Deadline.DueAt) {
			rows = append(rows, warning(label))
			if post.Deadline.Stage == "fund" {
				rows = append(rows, muted("Requester can cancel/reopen once active funding expires."))
			} else {
				rows = append(rows, muted("Contact the reviewer for a signed payment/refund decision."))
			}
		} else {
			rows = append(rows, muted(label))
		}
	}

	return rows
}
