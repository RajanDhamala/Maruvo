package tui

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/gorilla/websocket"
	"github.com/rajandhamala/Maruvo/cli/internal/api"
)

type workspaceLoaded struct {
	generation uint64
	workspace  api.Workspace
	err        error
}
type workspaceConnected struct {
	generation uint64
	stream     *api.WorkspaceStream
	err        error
}
type workspaceFrame struct {
	generation uint64
	frame      api.StreamFrame
	err        error
}
type workspaceRetry struct{ generation uint64 }
type workspaceActionResult struct {
	generation uint64
	notice     string
	err        error
}

func (m *model) stopWorkspace() {
	m.workspaceGen++
	if m.workspaceCancel != nil {
		m.workspaceCancel()
	}

	m.stream.Close()
	m.stream, m.workspaceCancel, m.workspaceCtx = nil, nil, nil
	m.workspaceAction, m.reviewConfirm, m.workspaceReview = "", false, false
	m.workspacePendingAction = ""
	m.reviewVersion = 0
}

func (m model) openWorkspace(action string) (tea.Model, tea.Cmd) {
	if m.workspace.Post.ID != m.posts[m.selected].ID || m.composer.root == "" {
		m.composer = chatComposer{draft: textField{limit: 4000}, root: projectDirectory()}
	}

	m.stopWorkspace()
	m.workspaceCtx, m.workspaceCancel = context.WithCancel(m.ctx)
	m.workspace = api.Workspace{}
	m.screen, m.loading, m.live = workspaceScreen, true, "Connecting..."
	m.err, m.notice, m.workspaceAction = nil, "", ""
	m.workspacePendingAction = action
	m.workspaceFiles, m.fileSelection, m.activityScroll = false, 0, 0
	postID, generation, ctx := m.posts[m.selected].ID, m.workspaceGen, m.workspaceCtx

	return m, func() tea.Msg {
		result, err := m.client.Workspace(ctx, m.token, postID)
		return workspaceLoaded{generation: generation, workspace: result, err: err}
	}
}

func (m model) workspaceLoaded(msg workspaceLoaded) (tea.Model, tea.Cmd) {
	if m.screen != workspaceScreen || msg.generation != m.workspaceGen {
		return m, nil
	}

	m.loading = false
	m.setPostError(msg.err)

	if msg.err != nil {
		m.live = "Unavailable"
		return m, nil
	}

	m.workspace, m.escrow = msg.workspace, msg.workspace.Escrow
	for i := range m.posts {
		if m.posts[i].ID == msg.workspace.Post.ID {
			m.posts[i] = msg.workspace.Post
		}
	}

	var compose tea.Cmd

	if action := m.workspacePendingAction; action != "" {
		m.workspacePendingAction = ""
		if action == "u" {
			next, cmd := m.startAttachment()
			m, compose = next.(model), cmd
		}
	}

	return m, tea.Batch(m.connectWorkspace(), compose)
}

func (m model) connectWorkspace() tea.Cmd {
	generation, ctx, postID, after := m.workspaceGen, m.workspaceCtx, m.workspace.Post.ID, m.workspace.State.LastEventID
	cursor := m.workspace.Cursor

	return func() tea.Msg {
		stream, err := m.client.ConnectWorkspace(ctx, m.token, postID, after, cursor)
		return workspaceConnected{generation: generation, stream: stream, err: err}
	}
}

func (m model) workspaceConnected(msg workspaceConnected) (tea.Model, tea.Cmd) {
	if m.screen != workspaceScreen || msg.generation != m.workspaceGen {
		msg.stream.Close()
		return m, nil
	}

	if msg.err != nil {
		var failure *api.Error
		if errors.As(msg.err, &failure) && failure.StatusCode == 409 {
			return m.openWorkspace("")
		}

		if errors.As(msg.err, &failure) &&
			(failure.StatusCode == 401 || failure.StatusCode == 403 || failure.StatusCode == 404) {
			m.stopWorkspace()
			m.setPostError(msg.err)
			m.live = "Disconnected"

			return m, nil
		}

		m.live = "Reconnecting..."

		return m, m.retryWorkspace()
	}

	m.stream, m.live = msg.stream, "Live"

	return m, m.readWorkspace()
}

func (m model) readWorkspace() tea.Cmd {
	stream, generation := m.stream, m.workspaceGen

	return func() tea.Msg {
		frame, err := stream.Read()
		return workspaceFrame{generation: generation, frame: frame, err: err}
	}
}

func (m model) retryWorkspace() tea.Cmd {
	generation := m.workspaceGen
	return tea.Tick(2*time.Second, func(time.Time) tea.Msg { return workspaceRetry{generation: generation} })
}

func (m model) workspaceFrame(msg workspaceFrame) (tea.Model, tea.Cmd) {
	if m.screen != workspaceScreen || msg.generation != m.workspaceGen {
		return m, nil
	}

	if msg.err != nil {
		m.stream.Close()

		m.stream = nil
		if websocket.IsCloseError(msg.err, websocket.ClosePolicyViolation) {
			m.setPostError(&api.Error{StatusCode: 401, Message: "session expired"})
			return m, nil
		}

		m.live = "Reconnecting..."

		return m, m.retryWorkspace()
	}

	if msg.frame.Event == "workspace.event" {
		var event api.WorkspaceEvent
		if err := json.Unmarshal(msg.frame.Data, &event); err != nil {
			m.err = err
		} else {
			m.applyWorkspaceEvent(event)
		}
	}

	return m, m.readWorkspace()
}

func (m *model) applyWorkspaceEvent(event api.WorkspaceEvent) {
	if event.PostID != m.workspace.Post.ID {
		return
	}

	if event.StreamID != "" {
		if !api.StreamCursorAfter(event.StreamID, m.workspace.Cursor) {
			return
		}

		m.workspace.Cursor = event.StreamID
		for _, existing := range m.workspace.Events {
			if existing.StreamID == event.StreamID {
				return
			}
		}
	}

	if event.ID > 0 {
		if event.ID <= m.workspace.State.LastEventID {
			return
		}

		m.workspace.State.LastEventID = event.ID
	}

	m.workspace.Events = append(m.workspace.Events, event)
	if len(m.workspace.Events) > 100 {
		m.workspace.Events = m.workspace.Events[len(m.workspace.Events)-100:]
	}

	var data struct {
		ID                string     `json:"id"`
		Name              string     `json:"name"`
		Size              int64      `json:"size"`
		SHA256            string     `json:"sha256"`
		Status            string     `json:"status"`
		State             string     `json:"state"`
		Signature         string     `json:"signature"`
		Note              string     `json:"note"`
		SubmittedAt       *time.Time `json:"submitted_at"`
		SubmissionVersion int64      `json:"submission_version"`
		ReviewState       string     `json:"review_state"`
		Purpose           string     `json:"purpose"`
		DeliveryFiles     []string   `json:"delivery_files"`
	}
	if json.Unmarshal(event.Data, &data) != nil {
		return
	}

	switch event.Kind {
	case "file.shared":
		for _, file := range m.workspace.Files {
			if file.ID == data.ID {
				return
			}
		}

		actorID := int64(0)
		if event.ActorID != nil {
			actorID = *event.ActorID
		}

		m.workspace.Files = append(
			m.workspace.Files,
			api.WorkspaceFile{
				ID:         data.ID,
				PostID:     event.PostID,
				UploadedBy: actorID,
				Name:       data.Name,
				Size:       data.Size,
				SHA256:     data.SHA256,
				CreatedAt:  event.CreatedAt,
				Purpose:    data.Purpose,
			},
		)
	case "escrow.updated":
		m.workspace.Escrow.State, m.workspace.Escrow.Signature = data.State, data.Signature
		m.escrow = m.workspace.Escrow
	case "task.status":
		m.workspace.Post.Status = data.Status
		for i := range m.posts {
			if m.posts[i].ID == event.PostID {
				m.posts[i].Status = data.Status
			}
		}
	case "work.submitted":
		m.workspace.State.Submission, m.workspace.State.SubmittedAt = data.Note, data.SubmittedAt
		m.workspace.State.SubmissionVersion, m.workspace.State.ReviewState, m.workspace.State.ReviewNote = data.SubmissionVersion, "submitted", ""
		m.workspace.State.DeliveryFiles = data.DeliveryFiles
	case "review.changes_requested":
		m.workspace.State.ReviewState, m.workspace.State.ReviewNote = "changes_requested", data.Note
	case "review.completed":
		m.workspace.State.ReviewState = data.ReviewState
		m.reviewConfirm = false
	case "settlement.updated":
		m.workspace.Settlement.State, m.workspace.Settlement.Note, m.workspace.Settlement.Signature = data.State, data.Note, data.Signature

		var decision struct {
			Action string `json:"action"`
		}

		_ = json.Unmarshal(event.Data, &decision)

		m.workspace.Settlement.Action, m.workspace.Settlement.SubmissionVersion = decision.Action, data.SubmissionVersion
		if data.State != "prepared" {
			m.reviewConfirm = false
		}
	}

	if (m.workspaceAction == "a" || m.workspaceAction == "x" || m.workspaceAction == "e") &&
		!m.reviewIsCurrent() {
		m.workspaceAction, m.workspaceInput, m.reviewConfirm = "", textField{}, false
		m.notice = "Delivery changed. Reopen review before deciding."
	}
}

func (m model) updateWorkspace(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	key := msg.String()

	if m.loading {
		return m, nil
	}

	if m.reviewConfirm {
		switch key {
		case "enter", "y":
			return m.signSettlement()
		case "esc", "n":
			m.reviewConfirm, m.workspaceAction = false, ""
			m.notice = "Decision not submitted. Reopen it or wait for expiry to change it."
		}

		return m, nil
	}

	if m.workspaceAction != "" {
		switch key {
		case "esc":
			m.workspaceAction, m.workspaceInput = "", textField{}
		case "enter":
			return m.performWorkspaceAction()
		default:
			m.workspaceInput.key(msg)
		}

		return m, nil
	}

	if m.composing() {
		return m.updateComposer(msg)
	}

	return m.workspaceCommand(key)
}

func (m model) workspaceCommand(key string) (tea.Model, tea.Cmd) {
	switch key {
	case "q":
		m.stopWorkspace()
		return m, tea.Quit
	case "esc":
		if m.workspaceFiles || m.workspaceReview {
			m.workspaceFiles, m.workspaceReview = false, false
			return m, nil
		}

		return m.openDetail()
	case "b":
		return m.openDetail()
	case "1":
		return m.openPosts(false)
	case "2":
		return m.openPosts(true)
	case "3":
		m.stopWorkspace()
		m.screen, m.form = newPostScreen, newPostForm()
	case "l":
		return m.beginLogout()
	case "p":
		m.profileOpen = true
	case "r":
		return m.openWorkspace("")
	case "tab":
		m.workspaceFiles = !m.workspaceFiles
		m.workspaceReview = false
		m.activityScroll = 0
	case "v":
		m.workspaceReview, m.workspaceFiles, m.activityScroll = !m.workspaceReview, false, 0
	case "up", "k":
		if m.workspaceFiles {
			m.fileSelection = max(0, m.fileSelection-1)
		} else {
			m.activityScroll++
		}
	case "down", "j":
		if m.workspaceFiles {
			m.fileSelection = min(max(0, len(m.workspace.Files)-1), m.fileSelection+1)
		} else {
			m.activityScroll = max(0, m.activityScroll-1)
		}
	case "m":
		m.workspaceFiles, m.workspaceReview = false, false
	case "u":
		return m.startAttachment()
	case "s":
		if m.workspace.Post.ID == 0 {
			return m, nil
		}

		if m.workspace.Post.Status == "completed" || m.workspace.Post.Status == "cancelled" {
			m.err = errors.New("This workspace is closed.")
			return m, nil
		}

		if !m.isWorker(m.workspace.Post) || m.workspace.Escrow.State != "confirmed" ||
			(m.workspace.State.ReviewState != "working" && m.workspace.State.ReviewState != "changes_requested") {
			m.err = errors.New("Only the worker can submit funded work or a requested revision.")
			return m, nil
		}

		m.workspaceAction, m.workspaceInput, m.err, m.notice = key, textField{limit: 4000}, nil, ""
	case "a", "x", "e":
		if !m.canReviewAction(key) {
			m.err = errors.New("Reviewer wallet approval is required; payout needs submitted, funded work.")
			return m, nil
		}

		m.workspaceAction, m.workspaceInput, m.err, m.notice = key, textField{limit: 4000}, nil, ""
		m.reviewVersion = m.workspace.State.SubmissionVersion
	case "d", "enter":
		if m.workspaceFiles && len(m.workspace.Files) > 0 {
			return m.downloadWorkspaceFile(m.fileSelection)
		}
	}

	return m, nil
}

func (m model) performWorkspaceAction() (tea.Model, tea.Cmd) {
	if (m.workspaceAction == "a" || m.workspaceAction == "x" || m.workspaceAction == "e") &&
		!m.reviewIsCurrent() {
		m.err = errors.New("Delivery changed. Reopen review before deciding.")
		return m, nil
	}

	text := strings.TrimSpace(m.workspaceInput.value)
	if text == "" {
		m.err = errors.New("Enter a value first.")
		return m, nil
	}

	if m.workspaceAction == "a" || m.workspaceAction == "x" {
		return m.prepareSettlement()
	}

	generation, action, postID, ctx := m.workspaceGen, m.workspaceAction, m.workspace.Post.ID, m.workspaceCtx
	reviewVersion := m.reviewVersion

	var file api.WorkspaceFile
	if action == "d" {
		file = m.workspace.Files[m.fileSelection]
	}

	m.loading, m.err, m.notice = true, nil, ""

	return m, func() tea.Msg {
		var err error

		notice := ""

		switch action {
		case "s":
			err = m.client.SubmitWork(ctx, m.token, postID, text)
			notice = "Work submitted for review."
		case "e":
			err = m.client.RequestChanges(ctx, m.token, postID, reviewVersion, text)
			notice = "Changes requested."
		case "d":
			err = m.client.DownloadFile(ctx, m.token, postID, file, text)
			notice = "Saved to " + text
		}

		return workspaceActionResult{generation: generation, notice: notice, err: err}
	}
}

func (m model) workspaceLayout() postLayout {
	width := m.contentWidth()
	_, height := m.dimensions()
	post := m.workspace.Post
	l := postLayout{footer: "Esc chat · Tab files · v review · b task"}
	l.rows = []string{
		align(bold(fmt.Sprintf("Workspace · #%d", post.ID)), muted(m.live), width),
		muted(plain(post.Title)),
		muted(
			"Escrow: " + m.workspace.Escrow.State + " · Task: " + strings.ReplaceAll(post.Status, "_", " "),
		),
	}

	l.rows = append(l.rows, muted("Delivery: "+strings.ReplaceAll(m.workspace.State.ReviewState, "_", " ")))
	if m.workspace.Settlement.State != "" {
		l.rows = append(
			l.rows,
			muted("Settlement: "+m.workspace.Settlement.Action+" · "+m.workspace.Settlement.State),
		)
	}

	if m.reviewConfirm {
		return m.reviewConfirmationLayout()
	}

	if m.workspaceAction != "" {
		if height < 20 {
			l.rows = l.rows[:1]
		}

		label, buttonLabel := "", ""

		switch m.workspaceAction {
		case "s":
			label, buttonLabel = "Delivery note · describe files or paste your commit URL", "Submit for review"
		case "a":
			label, buttonLabel = "Approval note · explain why the delivery is accepted", "Prepare payout"
		case "x":
			label, buttonLabel = "Refund note · explain why escrow should be returned", "Prepare refund"
		case "e":
			label, buttonLabel = "Requested changes · tell the worker what to revise", "Request changes"
		case "d":
			label, buttonLabel = "Save "+plain(
				m.workspace.Files[m.fileSelection].Name,
			)+" to a new local path", "Download"
		}

		l.rows = append(l.rows, "", bold(label), m.workspaceInput.input(width, true), "")
		l.buttons([]string{buttonLabel, "Cancel"}, []string{"workspace-send", "cancel"})

		l.footer = "Enter confirm   Esc cancel"
		if m.loading {
			l.footer = "Saving..."
		}

		return l
	}

	if post.ID != 0 && m.composing() {
		return m.chatLayout()
	}

	if m.loading {
		l.rows = append(l.rows, "", muted("Loading workspace..."))
		return l
	}

	if height < 20 {
		l.rows = l.rows[:1]
	}

	l.rows = append(l.rows, "")
	if m.workspaceReview {
		return m.reviewLayout(l)
	}

	if m.workspaceFiles {
		l.buttons([]string{"Chat", "Attach file", "Task details"}, []string{"m", "u", "b"})
		l.rows = append(l.rows, "")
		visible := max(1, m.bodyHeight()-len(l.rows))

		start := max(0, m.fileSelection-visible+1)
		for i := start; i < min(len(m.workspace.Files), start+visible); i++ {
			file := m.workspace.Files[i]

			tag := ""
			if file.Purpose == "input" || file.Purpose == "output" {
				tag = "[" + file.Purpose + "] "
			}

			for _, id := range m.workspace.State.DeliveryFiles {
				if id == file.ID {
					tag = fmt.Sprintf("[delivery v%d] ", m.workspace.State.SubmissionVersion)
					break
				}
			}

			label := fmt.Sprintf("  %s%s · %s", tag, plain(file.Name), fileSize(file.Size))
			if i == m.fileSelection {
				label = accent("› " + strings.TrimSpace(label))
			}

			l.hit(0, len(l.rows), max(1, width-12), 1, "workspace-file", i)
			l.hit(max(0, width-12), len(l.rows), 12, 1, "workspace-download", i)
			label = align(label, accent("↓ Download"), width)
			l.rows = append(l.rows, label)
		}

		if len(m.workspace.Files) == 0 {
			l.rows = append(l.rows, muted("No shared files yet. Use @filename in chat to attach one."))
		}

		l.footer = "↑↓ select · Enter download · u attach · Esc chat · b task"
	}

	return l
}

func (m model) downloadWorkspaceFile(index int) (tea.Model, tea.Cmd) {
	if index < 0 || index >= len(m.workspace.Files) {
		return m, nil
	}

	m.fileSelection = index

	name := filepath.Base(m.workspace.Files[index].Name)
	if name == "." || name == string(filepath.Separator) {
		name = "download"
	}

	destination := filepath.Join(m.composer.root, name)
	m.workspaceAction, m.workspaceInput = "d", textField{
		value:  destination,
		cursor: len([]rune(destination)),
		limit:  4000,
	}
	m.err, m.notice = nil, ""

	return m, nil
}

func (m model) eventLabel(event api.WorkspaceEvent) string {
	actor := "System"
	if event.ActorID != nil {
		actor = "User #" + strconv.FormatInt(*event.ActorID, 10)
		if strconv.FormatInt(*event.ActorID, 10) == m.user.ID {
			actor = "You"
		}
	}

	var data struct {
		Text   string `json:"text"`
		Name   string `json:"name"`
		Note   string `json:"note"`
		State  string `json:"state"`
		Status string `json:"status"`
	}

	_ = json.Unmarshal(event.Data, &data)
	switch event.Kind {
	case "message":
		return actor + ": " + plain(data.Text)
	case "file.shared":
		return actor + " shared " + plain(data.Name)
	case "work.submitted":
		return actor + " submitted work: " + plain(data.Note)
	case "review.changes_requested":
		return actor + " requested changes: " + plain(data.Note)
	case "review.completed":
		var result struct {
			ReviewState string `json:"review_state"`
		}

		_ = json.Unmarshal(event.Data, &result)

		return "Review: " + result.ReviewState
	case "settlement.updated":
		var decision struct {
			Action string `json:"action"`
		}

		_ = json.Unmarshal(event.Data, &decision)

		return "Settlement: " + decision.Action + " · " + data.State + " · " + plain(data.Note)
	case "task.accepted":
		return actor + " accepted this task."
	case "escrow.updated":
		return "Escrow: " + data.State
	case "task.status":
		return "Task: " + strings.ReplaceAll(data.Status, "_", " ")
	}

	return event.Kind
}
