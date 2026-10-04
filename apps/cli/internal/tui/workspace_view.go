package tui

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/charmbracelet/x/ansi"
)

type conversationRow struct {
	text      string
	fileIndex int
}

func fileSize(size int64) string {
	if size < 1024 {
		return fmt.Sprintf("%d B", size)
	}

	if size < 1<<20 {
		return fmt.Sprintf("%.1f KiB", float64(size)/1024)
	}

	return fmt.Sprintf("%.1f MiB", float64(size)/(1<<20))
}

func chatText(text string) string {
	return strings.Map(func(r rune) rune {
		if r == '\n' {
			return r
		}

		if r == '\t' {
			return ' '
		}

		if unicode.IsControl(r) {
			return -1
		}

		return r
	}, ansi.Strip(text))
}

func (m model) conversationRows(width int) []conversationRow {
	var rows []conversationRow

	for _, event := range m.workspace.Events {
		switch event.Kind {
		case "message", "file.shared":
			actor := "System"
			if event.ActorID != nil {
				actor = "User #" + strconv.FormatInt(*event.ActorID, 10)
				if strconv.FormatInt(*event.ActorID, 10) == m.user.ID {
					actor = "You"
				} else if *event.ActorID == m.workspace.Post.UserID {
					actor = "Requester"
				} else if m.workspace.Post.AcceptedBy != nil && *event.ActorID == *m.workspace.Post.AcceptedBy {
					actor = "Worker"
				}
			}

			var data struct {
				ID, Name, Text string
				Size           int64
			}

			_ = json.Unmarshal(event.Data, &data)
			label := bold(actor) + "  " + muted(event.CreatedAt.Local().Format("15:04"))
			rows = append(rows, conversationRow{text: label, fileIndex: -1})

			if event.Kind == "message" {
				for _, line := range strings.Split(ansi.Wrap(chatText(data.Text), max(1, width-2), ""), "\n") {
					rows = append(rows, conversationRow{text: "  " + line, fileIndex: -1})
				}
			} else {
				index := -1

				for i, file := range m.workspace.Files {
					if file.ID == data.ID {
						index = i
						break
					}
				}

				line := align(
					"  "+accent("▧ "+plain(data.Name))+"  "+muted(fileSize(data.Size)),
					accent("↓ Download"),
					width,
				)
				rows = append(rows, conversationRow{text: line, fileIndex: index})
			}

			rows = append(rows, conversationRow{text: "", fileIndex: -1})
		default:
			label := "· " + m.eventLabel(event)
			for _, line := range strings.Split(ansi.Wrap(label, max(1, width), ""), "\n") {
				rows = append(rows, conversationRow{text: muted(line), fileIndex: -1})
			}
		}
	}

	return rows
}

func (m model) chatLayout() postLayout {
	width, bodyHeight := m.contentWidth(), m.bodyHeight()
	post := m.workspace.Post
	l := postLayout{footer: "Enter send · @ attach · Ctrl+F files · Ctrl+R review · Esc task"}
	l.rows = []string{align(bold(fmt.Sprintf("#%d  %s", post.ID, plain(post.Title))), accent(m.live), width)}
	links := fmt.Sprintf("Files %d   Review   Task", len(m.workspace.Files))
	status := "Escrow " + m.workspace.Escrow.State + " · " + strings.ReplaceAll(
		m.workspace.State.ReviewState,
		"_",
		" ",
	)

	l.rows = append(l.rows, align(muted(status), muted(links), width))
	if m.workspace.Post.Deadline.DueAt != nil && !time.Now().Before(*m.workspace.Post.Deadline.DueAt) {
		l.rows[0] = align(bold(fmt.Sprintf("#%d  %s", post.ID, plain(post.Title))),
			warning(deadlineLabel(m.workspace.Post.Deadline)), width)
	}

	x := width - ansi.StringWidth(links)
	l.hit(x, 1, len(fmt.Sprintf("Files %d", len(m.workspace.Files))), 1, "workspace-tab", 0)
	l.hit(x+len(fmt.Sprintf("Files %d", len(m.workspace.Files)))+3, 1, 6, 1, "v", 0)
	l.hit(width-4, 1, 4, 1, "b", 0)

	composer := m.composerRows(width)

	var (
		suggestions       []string
		suggestionIndexes []int
	)

	if m.composer.completing {
		suggestions = append(
			suggestions,
			muted("Attach a local file · "+plain(filepath.Base(m.composer.root))),
		)
		suggestionIndexes = append(suggestionIndexes, -1)
		available := max(1, min(5, bodyHeight-len(l.rows)-len(composer)-1))

		start := max(0, m.composer.selection-available+1)
		for i := start; i < min(len(m.composer.suggestions), start+available); i++ {
			file := m.composer.suggestions[i]

			name := plain(file.label)
			if file.directory {
				name += "/"
			}

			label := "  " + name
			if i == m.composer.selection {
				label = accent("› " + name)
			}

			suggestions = append(suggestions, label)
			suggestionIndexes = append(suggestionIndexes, i)
		}

		if len(m.composer.suggestions) == 0 {
			label := "No matching files. Try a filename or @/absolute/path."
			if m.composer.fileError != "" {
				label = m.composer.fileError
			}

			suggestions = append(suggestions, muted("  "+plain(label)))
			suggestionIndexes = append(suggestionIndexes, -1)
		}

		l.footer = "↑↓ choose · Tab / Enter attach · Esc dismiss · files send with your message"
	}

	if m.loading {
		l.footer = "Sending message and attachments..."
	}

	visible := max(0, bodyHeight-len(l.rows)-len(composer)-len(suggestions))
	activity := m.conversationRows(width)
	offset := min(m.activityScroll, max(0, len(activity)-visible))

	end := len(activity) - offset
	for _, row := range activity[max(0, end-visible):end] {
		if row.fileIndex >= 0 {
			l.hit(max(0, width-12), len(l.rows), 12, 1, "workspace-download", row.fileIndex)
		}

		l.rows = append(l.rows, row.text)
	}

	if len(activity) == 0 && visible > 0 {
		l.rows = append(l.rows, muted("Start the conversation. Type a message or attach a file with @."))
	}

	for len(l.rows) < bodyHeight-len(composer)-len(suggestions) {
		l.rows = append(l.rows, "")
	}

	for i, line := range suggestions {
		if suggestionIndexes[i] >= 0 {
			l.hit(0, len(l.rows), width, 1, "local-file", suggestionIndexes[i])
		}

		l.rows = append(l.rows, line)
	}

	if len(m.composer.attachments) > 0 {
		x := 2

		for i, file := range m.composer.attachments {
			chipWidth := ansi.StringWidth("[" + plain(filepath.Base(file.path)) + " ×]")
			l.hit(x, len(l.rows)+1, min(chipWidth, max(0, width-x)), 1, "remove-attachment", i)
			x += chipWidth + 1
		}
	}

	l.rows = append(l.rows, composer...)

	return l
}
