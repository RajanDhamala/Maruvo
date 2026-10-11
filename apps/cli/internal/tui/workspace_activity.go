package tui

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/rajandhamala/Maruvo/cli/internal/api"
	"github.com/rajandhamala/Maruvo/cli/internal/providers"
)

type activityDetail struct {
	State  string `json:"state"`
	Detail string `json:"detail"`
}

func sameActivityActor(a, b api.WorkspaceEvent) bool {
	if a.Kind != "agent.activity" || b.Kind != "agent.activity" || a.PostID != b.PostID {
		return false
	}
	if a.ActorID == nil || b.ActorID == nil {
		return a.ActorID == nil && b.ActorID == nil
	}
	return *a.ActorID == *b.ActorID
}

func activityKey(event api.WorkspaceEvent) string {
	actor := int64(0)
	if event.ActorID != nil {
		actor = *event.ActorID
	}
	identity := event.CreatedAt.UTC().Format(time.RFC3339Nano)
	if event.CreatedAt.IsZero() {
		identity = event.StreamID
		if identity == "" {
			identity = strconv.FormatInt(event.ID, 10)
		}
	}
	return fmt.Sprintf("%d/%d/%s", event.PostID, actor, identity)
}

func (m model) activityActor(event api.WorkspaceEvent) string {
	if event.ActorID == nil {
		return "Agent"
	}
	switch {
	case strconv.FormatInt(*event.ActorID, 10) == m.user.ID:
		return "Your agent"
	case *event.ActorID == m.workspace.Post.UserID:
		return "Requester agent"
	case m.workspace.Post.AcceptedBy != nil && *event.ActorID == *m.workspace.Post.AcceptedBy:
		return "Worker agent"
	default:
		return fmt.Sprintf("User #%d agent", *event.ActorID)
	}
}

func activityText(event api.WorkspaceEvent) (string, string) {
	var data activityDetail
	_ = json.Unmarshal(event.Data, &data)
	state := strings.ReplaceAll(plain(data.State), "_", " ")
	detail := strings.TrimPrefix(plain(data.Detail), "… ")
	if detail == "" {
		detail = state
	} else if state != "" && state != "working" {
		detail = state + " · " + detail
	}
	return state, detail
}

func toolActivityDetail(event providers.Event) string {
	text := chatText(event.Text)
	if event.Preview != "" {
		text += "\n" + chatText(event.Preview)
	}
	chars := []rune(text)
	if len(chars) > 1000 {
		text = string(chars[:998]) + "\n…"
	}
	return text
}

type activityAction struct {
	index                                   int
	label, target, status, outcome, preview string
}

func parseActivityAction(index int, event api.WorkspaceEvent) (activityAction, bool) {
	var data activityDetail
	_ = json.Unmarshal(event.Data, &data)
	lines := strings.Split(chatText(data.Detail), "\n")
	heading := strings.TrimSpace(lines[0])
	status := ""
	for _, marker := range []struct{ prefix, status string }{{"… ", "running"}, {"✓ ", "succeeded"}, {"× ", "failed"}} {
		if strings.HasPrefix(heading, marker.prefix) {
			status = marker.status
			heading = strings.TrimPrefix(heading, marker.prefix)
			break
		}
	}
	label, target, _ := strings.Cut(heading, " · ")
	verb, _, _ := strings.Cut(label, " ")
	if (data.State != "working" && data.State != "failed") || !strings.Contains("|Read|List|Search|Write|Run|Share|Receive|Message|Submit|Request|", "|"+verb+"|") {
		return activityAction{}, false
	}
	action := activityAction{index: index, label: label, target: target, status: status}
	if len(lines) > 1 {
		action.preview = strings.TrimRight(strings.Join(lines[1:], "\n"), "\n")
		// Completed tool events put the result and duration before the preview.
		if status == "succeeded" || status == "failed" {
			action.outcome = strings.TrimSpace(lines[1])
		}
	}
	return action, true
}

func (m model) activityRows(start, end, width int) []conversationRow {
	events := m.workspace.Events[start:end]
	label := "Agent activity"
	explored := false
	var actions []activityAction
	for i, event := range events {
		action, ok := parseActivityAction(start+i, event)
		if !ok {
			continue
		}
		if strings.HasPrefix(action.label, "Read ") || strings.HasPrefix(action.label, "List ") || strings.HasPrefix(action.label, "Search ") {
			explored = true
		}
		if strings.HasPrefix(action.label, "Write ") || strings.HasPrefix(action.label, "Run ") || strings.HasPrefix(action.label, "Share ") {
			label = "Worked"
		}
		// Tools execute serially. Replace their running row with its result,
		// retaining the start identity so an open preview survives completion.
		if len(actions) > 0 && (action.status == "succeeded" || action.status == "failed") {
			previous := &actions[len(actions)-1]
			if previous.status == "running" && previous.label == action.label && previous.target == action.target {
				action.index = previous.index
				*previous = action
				continue
			}
		}
		actions = append(actions, action)
	}
	if label == "Agent activity" && explored {
		label = "Explored"
	}
	latest := events[len(events)-1]
	unit := "updates"
	if len(events) == 1 {
		unit = "update"
	}
	heading := "• " + bold(label) + muted(fmt.Sprintf(" · %s · %d %s", m.activityActor(latest), len(events), unit))
	rows := []conversationRow{{text: ansi.Truncate(heading, width, "…"), fileIndex: -1}}
	key := activityKey(events[0])
	visible := actions
	if !m.activityExpanded[key] && len(visible) > 3 {
		visible = visible[len(visible)-3:]
	}
	for _, action := range visible {
		rows = append(rows, m.activityActionRows(action, width)...)
	}
	state, detail := activityText(latest)
	if state != "working" || len(actions) == 0 {
		line := muted("  └ " + detail)
		if state == "failed" || state == "interrupted" {
			line = warning("  └ " + detail)
		}
		rows = append(rows, conversationRow{text: ansi.Truncate(line, width, "…"), fileIndex: -1})
	}
	if len(actions) > 3 || len(actions) == 0 {
		toggle := fmt.Sprintf("+ Show all %d actions", len(actions))
		if len(actions) == 0 {
			toggle = "+ Show details"
		}
		if m.activityExpanded[key] {
			toggle = "− Show fewer"
			if len(actions) == 0 {
				for _, event := range events {
					_, text := activityText(event)
					rows = append(rows, conversationRow{text: muted(ansi.Truncate("  │ "+event.CreatedAt.Local().Format("15:04")+"  "+text, width, "…")), fileIndex: -1})
				}
			}
		}
		rows = append(rows, conversationRow{text: "  " + muted(toggle), fileIndex: -1, activity: true, activityIndex: start})
	}
	return rows
}

func (m model) activityActionRows(action activityAction, width int) []conversationRow {
	key := "tool/" + activityKey(m.workspace.Events[action.index])
	expanded := m.activityExpanded[key]
	control := "+ See more"
	if expanded {
		control = "− See less"
	}
	text := "  " + accent(action.label)
	if action.target != "" {
		text += " · " + action.target
	}
	if action.outcome != "" {
		text += muted(" · " + action.outcome)
	}
	if action.status == "running" {
		text += muted(" · running")
	}
	if action.status == "failed" {
		text = warning("  " + action.label + " · " + action.target + " · " + action.outcome)
	}
	rows := []conversationRow{{text: align(text, muted(control), width), fileIndex: -1,
		activity: true, activityTool: true, activityIndex: action.index,
		activityX: width - ansi.StringWidth(control), activityWidth: ansi.StringWidth(control)}}
	if !expanded {
		return rows
	}
	rows[0] = conversationRow{text: ansi.Truncate(text, width, "…"), fileIndex: -1}
	preview := action.preview
	if preview == "" {
		preview = "Preview not recorded for this update."
		if action.status == "running" {
			preview = "Running; the result appears when this action finishes."
		}
	}
	lines := strings.Split(preview, "\n")
	for _, line := range lines[:min(7, len(lines))] {
		rows = append(rows, conversationRow{text: muted("  │ " + ansi.Truncate(line, max(1, width-4), "…")), fileIndex: -1})
	}
	rows = append(rows, conversationRow{text: "  " + muted(control), fileIndex: -1,
		activity: true, activityTool: true, activityIndex: action.index,
		activityX: 2, activityWidth: ansi.StringWidth(control)})
	return rows
}

func (m model) toggleWorkspaceActivity(index int) (tea.Model, tea.Cmd) {
	return m.toggleActivity(index, "")
}

func (m model) toggleWorkspaceTool(index int) (tea.Model, tea.Cmd) {
	return m.toggleActivity(index, "tool/")
}

func (m model) toggleActivity(index int, prefix string) (tea.Model, tea.Cmd) {
	if m.screen != workspaceScreen || !m.composing() || index < 0 || index >= len(m.workspace.Events) ||
		m.workspace.Events[index].Kind != "agent.activity" {
		return m, nil
	}
	key := prefix + activityKey(m.workspace.Events[index])
	if m.activityExpanded[key] {
		delete(m.activityExpanded, key)
	} else {
		if m.activityExpanded == nil {
			m.activityExpanded = make(map[string]bool)
		}
		m.activityExpanded[key] = true
	}
	return m, nil
}
