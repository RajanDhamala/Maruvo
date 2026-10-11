package tui

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/rajandhamala/Maruvo/cli/internal/api"
	workcli "github.com/rajandhamala/Maruvo/cli/internal/work"
)

type workBoard struct {
	open, busy, detail bool
	generation         uint64
	projects           []api.WorkProject
	project            api.WorkProject
	selection, scroll  int
	action             string
	labels             []string
	fields             []textField
	focus              int
	pending            map[string]any
	err                error
	cancel             context.CancelFunc
	prompt             string
	copyRequested      bool
}
type boardLoaded struct {
	generation uint64
	token      string
	projects   []api.WorkProject
	project    api.WorkProject
	err        error
}
type boardChanged struct {
	generation uint64
	token      string
	receipt    api.WorkReceipt
	err        error
}
type boardClock struct{ generation uint64 }
type boardTask struct {
	generation uint64
	token      string
	info       api.PostInfo
	err        error
}

func boardOperationID() string {
	var id [16]byte
	_, _ = rand.Read(id[:])
	return hex.EncodeToString(id[:])
}
func (m model) openBoard() (tea.Model, tea.Cmd) {
	if m.token == "" {
		m.commands.err = errors.New("Sign in to open your work projects.")
		return m, nil
	}
	if m.workAgent.active {
		m.commands.err = errors.New("Use /stop-work before opening a different work project.")
		return m, nil
	}
	m.commands.open = false
	m.board = workBoard{open: true, busy: true, generation: m.board.generation + 1}
	return m, m.fetchBoard()
}
func (m model) fetchBoard() tea.Cmd {
	generation, token, id := m.board.generation, m.token, m.board.project.ID
	return func() tea.Msg {
		if id == "" {
			projects, err := m.client.WorkProjects(m.ctx, token)
			return boardLoaded{generation: generation, token: token, projects: projects, err: err}
		}
		p, err := m.client.WorkProject(m.ctx, token, id)
		return boardLoaded{generation: generation, token: token, project: p, err: err}
	}
}
func (m model) boardTick() tea.Cmd {
	generation := m.board.generation
	return tea.Tick(5*time.Second, func(time.Time) tea.Msg { return boardClock{generation} })
}
func (m model) boardChange(req map[string]any) (tea.Model, tea.Cmd) {
	if m.board.busy {
		return m, nil
	}
	if req["operation_id"] == nil {
		req["operation_id"] = boardOperationID()
	}
	m.board.pending = req
	m.board.busy = true
	m.board.err = nil
	token, generation, project := m.token, m.board.generation, m.board.project.ID
	return m, func() tea.Msg {
		receipt, err := m.client.WorkOperation(m.ctx, token, project, req)
		return boardChanged{generation, token, receipt, err}
	}
}
func (m model) boardForm(action string, labels ...string) (tea.Model, tea.Cmd) {
	m.board.action = action
	m.board.labels = labels
	m.board.fields = make([]textField, len(labels))
	for i := range m.board.fields {
		m.board.fields[i] = textField{limit: 12000, byteLimit: 16000}
	}
	m.board.focus = 0
	m.board.pending = nil
	m.board.err = nil
	return m, nil
}
func (m model) boardAssignment() *api.WorkAssignment {
	if len(m.board.project.Assignments) == 0 {
		return nil
	}
	return &m.board.project.Assignments[min(m.board.selection, len(m.board.project.Assignments)-1)]
}
func (m model) boardCanAssign() bool {
	s := m.boardAssignment()
	if s == nil || s.ChildPost != 0 {
		return false
	}
	if fmt.Sprint(m.board.project.OwnerID) == m.user.ID {
		return true
	}
	for _, member := range m.board.project.Members {
		if fmt.Sprint(member.UserID) == m.user.ID && member.Status == "active" && (member.Role == "owner" || member.Role == "coordinator") {
			return true
		}
	}
	return false
}

func (m model) boardPromptRows() []string {
	return strings.Split(ansi.Wrap(ansi.Strip(m.board.prompt), m.contentWidth(), ""), "\n")
}
func (m model) submitBoardForm() (tea.Model, tea.Cmd) {
	if m.board.pending != nil {
		return m.boardChange(m.board.pending)
	}
	value := func(i int) string {
		if i >= len(m.board.fields) {
			return ""
		}
		return strings.TrimSpace(m.board.fields[i].value)
	}
	number := func(i int) int64 { id, _ := strconv.ParseInt(value(i), 10, 64); return id }
	action := m.board.action
	req := map[string]any{"action": action}
	s := m.boardAssignment()
	if s != nil {
		req["assignment_id"] = s.ID
	}
	switch action {
	case "create":
		req["name"] = value(0)
		if value(1) == "yes" && m.workspace.Post.ID > 0 {
			req["post_id"] = m.workspace.Post.ID
		}
	case "invite":
		req["user_id"] = number(0)
		req["role"] = value(1)
		if req["role"] == "" {
			req["role"] = "contributor"
		}
	case "remove":
		req["user_id"] = number(0)
	case "plan":
		kind := value(4)
		if kind == "" {
			kind = "work"
		}
		assignee := number(3)
		if assignee == 0 {
			assignee, _ = strconv.ParseInt(m.user.ID, 10, 64)
		}
		req["assignments"] = []map[string]any{{"id": value(0), "title": value(1), "instructions": value(2), "assignee": assignee, "kind": kind, "dependencies": strings.Fields(value(5)), "inputs": strings.Fields(value(6)), "expected_outputs": strings.Fields(value(7))}}
	case "assign":
		req["user_id"] = number(0)
	case "message":
		req["note"] = value(0)
	case "control":
		req["mode"] = value(0)
	case "integrate":
		req["base_revision"] = m.board.project.Revision
		req["note"] = value(0)
		req["outputs"] = strings.Fields(value(1))
		selected := []string{}
		for _, s := range m.board.project.Assignments {
			if s.State == "accepted" {
				selected = append(selected, s.Accepted)
			}
		}
		req["contributions"] = selected
	case "delegate":
		lamports, err := strconv.ParseInt(value(0), 10, 64)
		if err != nil {
			m.board.err = err
			return m, nil
		}
		end, err := time.Parse(time.RFC3339, value(1))
		if err != nil {
			m.board.err = err
			return m, nil
		}
		req["cost_lamports"] = lamports
		req["end_time"] = end
	case "collect":
		req["submission_version"] = number(0)
		if value(1) != "" {
			req["evidence"] = []api.WorkEvidence{{Command: value(1), ExitCode: int(number(2)), Result: value(3), Environment: value(4)}}
		}
	case "grant":
		destination := value(2)
		if !filepath.IsAbs(destination) {
			destination = filepath.Join(m.localDirectory(), destination)
		}
		m.board.busy = true
		generation, token, project := m.board.generation, m.token, m.board.project.ID
		request := map[string]any{"operation_id": boardOperationID(), "name": value(0), "permissions": strings.Fields(value(1))}
		return m, func() tea.Msg {
			requestFile, err := os.CreateTemp("", "maruvo-work-grant-*.json")
			if err != nil {
				return boardChanged{generation: generation, token: token, err: err}
			}
			defer os.Remove(requestFile.Name())
			err = json.NewEncoder(requestFile).Encode(request)
			_ = requestFile.Close()
			if err == nil {
				err = workcli.Run(m.ctx, m.client, m.profile, []string{"grant", "--project", project, "--file", requestFile.Name(), "--to", destination}, io.Discard, io.Discard)
			}
			return boardChanged{generation: generation, token: token, err: err}
		}
	case "revoke":
		req["grant_id"] = value(0)
	case "import":
		req["source_file"] = value(0)
	case "upload":
		path := value(0)
		if !filepath.IsAbs(path) {
			path = filepath.Join(m.localDirectory(), path)
		}
		f, err := os.Open(path)
		if err != nil {
			m.board.err = err
			return m, nil
		}
		content, err := io.ReadAll(io.LimitReader(f, (10<<20)+1))
		_ = f.Close()
		if err != nil || len(content) == 0 || len(content) > 10<<20 {
			m.board.err = errors.New("File must contain 1 byte through 10 MiB.")
			return m, nil
		}
		delete(req, "assignment_id")
		req["artifact_name"] = filepath.Base(path)
		req["content"] = content
	case "deliver":
		if len(m.board.project.Integrations) == 0 || m.board.project.PostID == 0 {
			m.board.err = errors.New("Integrate accepted contributions in an attached project first.")
			return m, nil
		}
		m.board.busy = true
		token, generation, project, post := m.token, m.board.generation, m.board.project.ID, m.board.project.PostID
		integration := m.board.project.Integrations[len(m.board.project.Integrations)-1].ID
		note := value(0)
		operation := boardOperationID()
		return m, func() tea.Msg {
			ws, err := m.client.Workspace(m.ctx, token, post)
			var receipt api.WorkReceipt
			if err == nil {
				receipt, err = m.client.WorkOperation(m.ctx, token, project, map[string]any{"action": "deliver", "operation_id": operation, "integration_id": integration, "submission_version": ws.State.SubmissionVersion, "inputs": ws.Context.InputFileIDs, "note": note})
			}
			return boardChanged{generation, token, receipt, err}
		}

	case "run":
		if s == nil {
			return m, nil
		}
		if value(0) == "" || value(1) == "" {
			m.board.err = errors.New("Choose your harness executable and attempt parent directory.")
			return m, nil
		}
		runCtx, cancel := context.WithCancel(m.ctx)
		m.board.cancel = cancel
		m.board.busy = true
		m.board.err = nil
		token, generation, project, assignment, executable, directory := m.token, m.board.generation, m.board.project.ID, s.ID, value(0), value(1)
		return m, func() tea.Msg {
			err := workcli.Execute(runCtx, m.client, token, project, assignment, boardOperationID(), executable, nil, directory, 30*time.Minute, io.Discard, io.Discard)
			cancel()
			return boardChanged{generation: generation, token: token, err: err}
		}
	case "download":
		id, destination := value(0), value(1)
		if !filepath.IsAbs(destination) {
			destination = filepath.Join(m.localDirectory(), destination)
		}
		m.board.busy = true
		generation, token, project := m.board.generation, m.token, m.board.project.ID
		return m, func() tea.Msg {
			err := workcli.Run(m.ctx, m.client, m.profile, []string{"download", "--project", project, "--id", id, "--to", destination}, io.Discard, io.Discard)
			return boardChanged{generation: generation, token: token, err: err}
		}
	default:
		m.board.err = errors.New("Unknown work action.")
		return m, nil
	}
	return m.boardChange(req)
}
func (m model) updateBoard(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	if m.board.busy {
		if msg.String() == "esc" && m.board.cancel != nil {
			m.board.cancel()
		}
		return m, nil
	}
	if m.board.action == "handoff" {
		switch msg.String() {
		case "esc":
			m.board.action, m.board.prompt, m.board.scroll = "", "", 0
		case "c":
			m.board.copyRequested = true
			return m, tea.SetClipboard(m.board.prompt)
		case "up", "k":
			m.board.scroll = max(0, m.board.scroll-1)
		case "down", "j":
			m.board.scroll++
		case "pgup":
			m.board.scroll = max(0, m.board.scroll-max(1, m.bodyHeight()-3))
		case "pgdown":
			m.board.scroll += max(1, m.bodyHeight()-3)
		case "home":
			m.board.scroll = 0
		case "end":
			m.board.scroll = len(m.boardPromptRows())
		}
		m.board.scroll = min(m.board.scroll, max(0, len(m.boardPromptRows())-max(1, m.bodyHeight()-3)))
		return m, nil
	}
	if m.board.action != "" {
		switch msg.String() {
		case "esc":
			m.board.action = ""
			m.board.pending = nil
		case "tab", "down":
			m.board.focus = min(len(m.board.fields), m.board.focus+1)
		case "shift+tab", "up":
			m.board.focus = max(0, m.board.focus-1)
		case "enter":
			if m.board.focus < len(m.board.fields) {
				m.board.focus++
			} else {
				return m.submitBoardForm()
			}
		default:
			if m.board.focus < len(m.board.fields) {
				before := m.board.fields[m.board.focus].value
				m.board.fields[m.board.focus].key(msg)
				if before != m.board.fields[m.board.focus].value {
					m.board.pending = nil
				}
			}
		}
		return m, nil
	}
	if m.board.project.ID == "" {
		switch msg.String() {
		case "esc":
			m.board.open = false
			m.board.generation++
		case "up", "k":
			m.board.selection = max(0, m.board.selection-1)
		case "down", "j":
			m.board.selection = min(max(0, len(m.board.projects)-1), m.board.selection+1)
		case "n":
			return m.boardForm("create", "Project name", "Attach the open paid task? Type yes (worker only)")
		case "enter", "a":
			if len(m.board.projects) == 0 {
				return m, nil
			}
			p := m.board.projects[m.board.selection]
			for _, member := range p.Members {
				if fmt.Sprint(member.UserID) == m.user.ID && member.Status == "invited" {
					m.board.project = p
					return m.boardChange(map[string]any{"action": "join"})
				}
			}
			m.board.project = p
			m.board.selection = 0
			m.board.busy = true
			return m, m.fetchBoard()
		case "r":
			m.board.busy = true
			return m, m.fetchBoard()
		}
		return m, nil
	}
	s := m.boardAssignment()
	switch msg.String() {
	case "esc":
		if m.board.detail {
			m.board.detail = false
			m.board.scroll = 0
		} else {
			m.board.project = api.WorkProject{}
			m.board.selection = 0
			m.board.busy = true
			return m, m.fetchBoard()
		}
	case "up", "k":
		if m.board.detail {
			m.board.scroll = max(0, m.board.scroll-1)
		} else {
			m.board.selection = max(0, m.board.selection-1)
		}
	case "down", "j":
		if m.board.detail {
			m.board.scroll++
		} else {
			m.board.selection = min(max(0, len(m.board.project.Assignments)-1), m.board.selection+1)
		}
	case "enter":
		m.board.detail = true
	case "r":
		m.board.busy = true
		return m, m.fetchBoard()
	case "n":
		return m.boardForm("plan", "Assignment ID", "Title", "Instructions", "Assignee user ID (blank: you)", "Kind: work or verification", "Dependency assignment IDs, space separated", "Approved input artifact IDs, space separated", "Output filenames, space separated")
	case "i":
		return m.boardForm("invite", "Operator user ID", "Role: contributor or coordinator")
	case "o":
		return m.boardForm("remove", "Operator user ID to revoke")
	case "t":
		return m.boardForm("control", "Project mode: paused, active, cancelled (revokes harnesses on pause)")
	case "g":
		return m.boardForm("integrate", "Integration / delivery note", "Accepted output artifact IDs, space separated")
	case "u":
		return m.boardForm("upload", "Local file to share as a project input")
	case "f":
		return m.boardForm("import", "Requester file ID to explicitly redistribute to project assignments")
	case "h":
		return m.boardForm("grant", "Harness name", "Permissions: read execute upload message (space separated)", "New credential file path")
	case "z":
		return m.boardForm("revoke", "Your harness grant ID to revoke")
	case "s":
		return m.boardForm("deliver", "Final delivery note (includes exact contributions and evidence)")
	case "d":
		return m.boardForm("download", "Artifact ID", "New destination path")
	case "m":
		if s != nil {
			return m.boardForm("message", "Message for this assignment")
		}
	case "w":
		if m.boardCanAssign() {
			return m.boardForm("assign", "Active member's Maruvo user ID")
		}
		m.board.err = errors.New("Only the lead/coordinator can reassign project work. Paid child assignments retain their directed worker.")
	case "q":
		if s != nil {
			m.board.action = "handoff"
			m.board.prompt = workcli.HandoffPrompt(m.client.URL(), m.board.project.ID, *s)
			m.board.scroll, m.board.copyRequested, m.board.err = 0, false, nil
		}
	case "x":
		if s != nil {
			return m.boardForm("run", "Your harness executable (Enter authorizes local execution)", "Parent directory for fresh isolated attempts")
		}
	case "a":
		if s != nil && len(s.Contributions) > 0 {
			return m.boardChange(map[string]any{"action": "accept", "assignment_id": s.ID, "contribution_id": s.Contributions[len(s.Contributions)-1].ID})
		}
	case "y":
		if s != nil {
			return m.boardChange(map[string]any{"action": "retry", "assignment_id": s.ID})
		}
	case "c":
		if s != nil {
			return m.boardChange(map[string]any{"action": "cancel", "assignment_id": s.ID})
		}
	case "b":
		if s != nil {
			return m.boardForm("delegate", "Separate child payment in lamports", "Acceptance cutoff, RFC3339 (e.g. 2026-12-01T12:00:00Z)")
		}
	case "v":
		if s != nil {
			return m.boardForm("collect", "Exact child submission version", "Reported verification command (optional)", "Reported exit code", "Reported result", "Reported environment")
		}
	case "p":
		post := m.board.project.PostID
		if s != nil && s.ChildPost != 0 {
			post = s.ChildPost
		}
		if post == 0 {
			m.board.err = errors.New("No paid task is attached.")
			return m, nil
		}
		m.board.busy = true
		generation, token := m.board.generation, m.token
		return m, func() tea.Msg {
			info, err := m.client.PostInfo(m.ctx, token, post)
			return boardTask{generation, token, info, err}
		}
	}
	return m, nil
}
func (m model) boardLayout() postLayout {
	rows := []string{bold("Work projects"), muted("Personal projects and paid tasks share assignments and contribution review."), ""}
	l := postLayout{footer: "↑↓ select/scroll · Enter inspect · n create · r refresh · Esc back"}
	if m.board.project.ID != "" {
		rows = []string{}
	}
	if m.board.action == "handoff" {
		l.rows = []string{bold("Agent instructions · paste into Codex or Claude")}
		l.wrappedButtons(m.contentWidth(), []string{"c Copy prompt", "Esc Back"}, []string{"c", "esc"})
		rows = l.rows
		if m.board.copyRequested {
			rows = append(rows, muted("Clipboard request sent. Terminal clipboard support is required."))
		} else {
			rows = append(rows, muted("Copy uses your terminal clipboard; work handoff also returns this prompt."))
		}
		prompt := m.boardPromptRows()
		room := max(1, m.bodyHeight()-len(rows))
		scroll := min(m.board.scroll, max(0, len(prompt)-room))
		rows = append(rows, prompt[scroll:min(len(prompt), scroll+room)]...)
		l.footer = "c copy prompt · ↑↓ / PgUp/PgDn scroll · Esc back"
	} else if m.board.action != "" {
		rows = append(rows, bold(strings.Title(m.board.action)))
		if m.board.action == "assign" {
			if s := m.boardAssignment(); s != nil {
				rows = append(rows, muted(plain(s.ID)+fmt.Sprintf(" · currently assigned to user %d", s.Assignee)))
			}
			rows = append(rows, muted("Invite with i; they must join before assignment."))
		}
		if m.board.focus < len(m.board.fields) {
			rows = append(rows, plain(m.board.labels[m.board.focus]))
			text, _ := m.board.fields[m.board.focus].textRows(m.contentWidth(), 3, true, "")
			rows = append(rows, text...)
		} else {
			for i, label := range m.board.labels {
				rows = append(rows, plain(label+": "+m.board.fields[i].value))
			}
			rows = append(rows, accent("Enter confirms · Esc cancels"))
		}
	} else if m.board.project.ID == "" {
		for i, p := range m.board.projects {
			prefix := "  "
			if i == m.board.selection {
				prefix = "› "
			}
			rows = append(rows, plain(prefix+p.Name+"  "+p.ID))
			for _, member := range p.Members {
				if fmt.Sprint(member.UserID) == m.user.ID && member.Status == "invited" {
					rows = append(rows, accent("  Invitation · Enter to join"))
				}
			}
		}
		if len(m.board.projects) == 0 {
			rows = append(rows, muted("No projects yet. Press n to create one."))
		}
	} else {
		p := m.board.project
		rows = append(rows, bold(plain(p.Name)), muted(fmt.Sprintf("%s · revision %d · concurrency %d", p.Status, p.Revision, p.Concurrency)))
		if m.boardAssignment() != nil {
			labels, actions := []string{}, []string{}
			if m.boardCanAssign() {
				labels, actions = append(labels, "w Assign user"), append(actions, "w")
			}
			labels, actions = append(labels, "q Agent instructions"), append(actions, "q")
			l.rows = rows
			l.wrappedButtons(m.contentWidth(), labels, actions)
			rows = l.rows
			l.footer = "q agent instructions · ↑↓ select/scroll · Esc back"
			if m.boardCanAssign() {
				l.footer = "w assign user · " + l.footer
			}
		}
		if m.board.detail && m.boardAssignment() != nil {
			s := m.boardAssignment()
			details := []string{bold(plain(s.Title)), plain(s.Instructions), fmt.Sprintf("%s · owner %d · %s", s.ID, s.Assignee, s.State), "Dependencies: " + strings.Join(s.Dependencies, ", ")}
			if s.Blocker != "" {
				details = append(details, warning(plain(s.Blocker)), "Next: "+plain(s.NextAction))
			}
			for _, cp := range s.Contributions {
				details = append(details, fmt.Sprintf("Contribution %s · %s · base %d", cp.ID, cp.Status, cp.BaseRevision), plain(cp.Note), "Exact inputs: "+strings.Join(cp.Inputs, ", "), "Outputs: "+strings.Join(cp.Outputs, ", "))
				for _, e := range cp.Evidence {
					details = append(details, plain(fmt.Sprintf("Reported check: %s (exit %d)", e.Command, e.ExitCode)), plain(e.Result), plain(e.Environment))
				}
			}
			for _, msg := range s.Thread {
				details = append(details, plain(fmt.Sprintf("%d: %s", msg.ActorID, msg.Text)))
			}
			for _, art := range p.Artifacts {
				details = append(details, plain(fmt.Sprintf("%s · %s · %d bytes", art.ID, art.Name, art.Size)))
			}
			m.board.scroll = min(m.board.scroll, max(0, len(details)-1))
			rows = append(rows, details[m.board.scroll:]...)
		} else {
			for i, s := range p.Assignments {
				prefix := "  "
				if i == m.board.selection {
					prefix = "› "
				}
				rows = append(rows, plain(fmt.Sprintf("%s%s · %s · operator %d", prefix, s.ID, s.State, s.Assignee)))
				if s.Blocker != "" {
					rows = append(rows, warning("  "+plain(s.Blocker)))
				}
			}
			for _, member := range p.Members {
				rows = append(rows, muted(fmt.Sprintf("Member %d · %s · %s", member.UserID, member.Role, member.Status)))
			}
			if len(p.Integrations) > 0 {
				latest := p.Integrations[len(p.Integrations)-1]
				rows = append(rows, accent(fmt.Sprintf("Integrated revision %d · %d contributions · %d artifacts", latest.Revision, len(latest.Contributions), len(latest.Outputs))), muted("Integration "+latest.ID))
			}
			if len(p.OutstandingChildren) > 0 {
				rows = append(rows, warning(fmt.Sprintf("Outstanding child obligations: %v", p.OutstandingChildren)))
			}
			rows = append(rows, muted("i invite · n assignment · w assign · o revoke member · t takeover"), muted("a accept · y retry · g integrate · b paid child · v collect · p payment/review"), muted("x run your harness · m message · d download · Enter inspect"))
		}
	}
	if m.board.busy {
		if m.board.cancel != nil {
			rows = append(rows, muted("Running your harness… Esc cancels."))
		} else {
			rows = append(rows, muted("Updating work state…"))
		}
	}
	if m.board.err != nil {
		rows = append(rows[:min(len(rows), max(0, m.bodyHeight()-1))], warning(plain(m.board.err.Error())))
	}
	l.rows = rows
	return l
}

func (m model) boardView() tea.View {
	l := m.boardLayout()
	return m.frame(l.rows, l.footer)
}

func (m model) updateBoardMouse(msg tea.MouseClickMsg) (tea.Model, tea.Cmd) {
	width, height := m.dimensions()
	if msg.Button != tea.MouseLeft || m.board.busy || width < 48 || height < 16 {
		return m, nil
	}
	x, y := msg.X-m.contentX(), msg.Y-m.bodyStart()
	if y < 0 || y >= m.bodyHeight() {
		return m, nil
	}
	for _, hit := range m.boardLayout().hits {
		if x >= hit.x && x < hit.x+hit.width && y == hit.y {
			if hit.action == "esc" {
				return m.updateBoard(tea.KeyPressMsg{Code: tea.KeyEscape})
			}
			return m.updateBoard(tea.KeyPressMsg{Text: hit.action})
		}
	}
	return m, nil
}
