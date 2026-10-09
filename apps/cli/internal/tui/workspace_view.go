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

func (m model) workspacePeerLabel() string {
	p := m.workspace.Post
	label, online := "Requester", false
	if m.isPoster(p) {
		label = "Worker"
	}
	if m.presence == nil || m.live != "Live" {
		return label + " · presence unavailable"
	}
	if m.isPoster(p) {
		online = m.presence.WorkerOnline
	} else {
		online = m.presence.RequesterOnline
	}
	if online {
		return label + " · online in workspace"
	}
	return label + " · away from workspace"
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
				AgentName      string `json:"agent_name"`
			}

			_ = json.Unmarshal(event.Data, &data)
			if data.AgentName != "" {
				actor += " · agent " + plain(data.AgentName)
			}

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
	l := postLayout{
		footer: "Enter send · / commands · @ attach · Ctrl+F files · Ctrl+R review · Ctrl+T controls · Esc task",
	}

	headerStatus := accent(m.live)
	if m.live == "Live" {
		headerStatus = accent("Connected")
	}

	if m.workspace.Escrow.Network == "devnet" {
		headerStatus = accent("Devnet · test SOL") + " · " + headerStatus
	}
	l.rows = []string{align(bold(fmt.Sprintf("#%d  %s", post.ID, plain(post.Title))), headerStatus, width)}
	links := fmt.Sprintf("Files %d   Review   Task", len(m.workspace.Files))
	flow := m.workspaceFlow()
	if post.Deadline.DueAt != nil && !time.Now().Before(*post.Deadline.DueAt) {
		flow.stage += " · overdue"
	}

	l.rows = append(l.rows, align(accent(flow.stage), muted(links), width))
	if bodyHeight >= 10 {
		label, button := "Your agent is stopped", "Set up agents"
		if m.workAgent.postID == post.ID && m.workAgent.status != "" {
			label = m.workAgent.status
		}
		if m.workSetup.armed && !m.workAgent.active {
			label, button = "You are ready · waiting for the other participant", "Cancel readiness"
		}
		if m.workAgent.active {
			label, button = "Your agent: "+m.workAgent.status, "Stop agent"
		}
		l.hit(max(0, width-len(button)), len(l.rows), len(button), 1, "workspace-agent", 0)
		l.rows = append(l.rows, align(muted(label), accent(button), width))
	}

	if bodyHeight >= 10 {
		controlLabel := m.workspacePeerLabel()
		if m.workspace.AgentControl.Mode == "manual" {
			controlLabel += " · agent actions paused"
		}

		l.hit(width-6, len(l.rows), 6, 1, "workspace-control", 0)
		l.rows = append(l.rows, align(muted(controlLabel), accent("Agents"), width))
	}

	if bodyHeight < 10 {
		l.rows[0] = align(bold(fmt.Sprintf("#%d  %s", post.ID, plain(post.Title))), muted(m.workspacePeerLabel()), width)
	}
	if bodyHeight >= 12 && m.workSetup.postID == post.ID && m.workSetup.peerStatus != "" {
		role, detail := "Requester", m.workSetup.peerDetail
		if m.isPoster(post) {
			role = "Worker"
			if m.workSetup.peerStatus == "working" && post.Remote.Detail != "" {
				detail = post.Remote.Detail
			}
		}
		l.rows = append(l.rows, muted(role+" agent: "+strings.ReplaceAll(m.workSetup.peerStatus, "_", " ")+" · "+plain(detail)))
	}
	if bodyHeight >= 12 && m.workSetup.peerStatus == "" && (post.Remote.AgentName != "" || post.Remote.Status == "working" || post.Remote.Status == "interrupted" || post.Remote.Status == "failed") {
		label := "Worker agent: " + strings.ReplaceAll(post.Remote.Status, "_", " ")
		if post.Remote.AgentName != "" {
			label += " · " + plain(post.Remote.AgentName)
		}
		if post.Remote.Detail != "" {
			label += " · " + plain(post.Remote.Detail)
		}
		l.rows = append(l.rows, muted(label))
	}

	x := width - ansi.StringWidth(links)
	l.hit(x, 1, len(fmt.Sprintf("Files %d", len(m.workspace.Files))), 1, "workspace-tab", 0)
	l.hit(x+len(fmt.Sprintf("Files %d", len(m.workspace.Files)))+3, 1, 6, 1, "v", 0)
	l.hit(width-4, 1, 4, 1, "b", 0)

	composer := m.composerRows(width)
	guidance := flowRows(flow, width)[1:]
	if bodyHeight-len(l.rows)-len(composer) >= len(guidance)+2 {
		l.rows = append(l.rows, guidance...)
	}
	if flow.label != "" && bodyHeight-len(l.rows)-len(composer) >= 1 {
		l.wrappedButtons(width, []string{flow.label}, []string{flow.action})
	}

	var (
		suggestions       []string
		suggestionIndexes []int
	)

	if m.composer.completing {
		space := bodyHeight - len(l.rows) - len(composer)
		if space > 1 {
			suggestions = append(
				suggestions,
				muted("Attach a local file · "+plain(filepath.Base(m.composer.root))),
			)
			suggestionIndexes = append(suggestionIndexes, -1)
		}

		available := max(0, min(5, space-len(suggestions)))

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

		if len(m.composer.suggestions) == 0 && available > 0 {
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
		label := "Messages and shared files appear here."
		if m.presence != nil && m.live == "Live" &&
			((m.isPoster(post) && !m.presence.WorkerOnline) || (!m.isPoster(post) && !m.presence.RequesterOnline)) {
			label = "They are away. Leave a message; it stays in this workspace."
		}
		l.rows = append(l.rows, muted(ansi.Truncate(label, width, "…")))
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
	sendWidth := ansi.StringWidth(" Enter send ")
	l.hit(width-sendWidth-2, len(l.rows)-2, sendWidth, 1, "workspace-chat-send", 0)
	if bodyHeight < 12 {
		l.hits[len(l.hits)-1].y = len(l.rows) - 1
	}

	return l
}
