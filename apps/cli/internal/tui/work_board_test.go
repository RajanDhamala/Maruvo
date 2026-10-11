package tui

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/rajandhamala/Maruvo/cli/internal/api"
)

func TestWorkBoardInvitationsAcceptanceAndGenerationGuards(t *testing.T) {
	var action string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req map[string]any
		_ = json.NewDecoder(r.Body).Decode(&req)
		action, _ = req["action"].(string)
		_ = json.NewEncoder(w).Encode(api.WorkReceipt{ProjectID: "project", OperationID: "fixture"})
	}))
	defer server.Close()
	m := model{ctx: t.Context(), client: api.NewClient(server.URL), token: "operator", user: api.User{ID: "2"}, width: 80, height: 24, board: workBoard{open: true, generation: 4, projects: []api.WorkProject{{ID: "project", Members: []api.WorkMember{{UserID: 2, Status: "invited"}}}}}}
	next, cmd := m.updateBoard(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = next.(model)
	if cmd == nil || m.board.project.ID != "project" {
		t.Fatal("invitation was not accepted through project operations")
	}
	_ = cmd()
	if action != "join" {
		t.Fatal("invitation used wrong operation")
	}
	before := m.board.project
	next, _ = m.Update(boardLoaded{generation: 3, token: "operator", project: api.WorkProject{ID: "stale"}})
	m = next.(model)
	if m.board.project.ID != before.ID {
		t.Fatal("stale board load replaced current project")
	}
	next, _ = m.Update(boardLoaded{generation: 4, token: "another-account", project: api.WorkProject{ID: "other"}})
	m = next.(model)
	if m.board.project.ID != before.ID {
		t.Fatal("foreign account board result was applied")
	}
}

func TestWorkBoardAssignmentHandoffControlsAtSmallAndLargeSizes(t *testing.T) {
	for _, size := range [][2]int{{48, 16}, {80, 24}, {168, 48}} {
		t.Run(fmt.Sprintf("%dx%d", size[0], size[1]), func(t *testing.T) {
			m := model{ctx: t.Context(), client: api.NewClient("https://maruvo.example"), screen: feedScreen, token: "private-login", profile: "sender-profile", user: api.User{ID: "1"}, width: size[0], height: size[1], board: workBoard{open: true, project: api.WorkProject{ID: "project", OwnerID: 1, Name: "Bug fix", Status: "active", Assignments: []api.WorkAssignment{{ID: "patch", Assignee: 42, Instructions: "private brief", State: "queued"}}}}}
			view := strings.Split(ansi.Strip(m.boardView().Content), "\n")
			if len(view) != size[1] {
				t.Fatalf("board frame exceeds terminal: %d rows", len(view))
			}
			for _, action := range []string{"w", "q"} {
				var target *hitArea
				layout := m.boardLayout()
				for i := range layout.hits {
					if layout.hits[i].action == action {
						target = &layout.hits[i]
					}
				}
				if target == nil || target.y >= m.bodyHeight() {
					t.Fatalf("%s action is clipped or missing", action)
				}
				y, x := target.y+m.bodyStart(), target.x+m.contentX()
				if !strings.Contains(view[y], map[string]string{"w": "Assign user", "q": "Agent instructions"}[action]) || x >= ansi.StringWidth(view[y]) {
					t.Fatalf("%s click target is not on its visible button", action)
				}
				next, cmd := m.Update(tea.MouseClickMsg{X: x, Y: y, Button: tea.MouseLeft})
				opened := next.(model)
				if cmd != nil {
					t.Fatal("opening assignment controls invoked a remote operation")
				}
				if action == "w" {
					if opened.board.action != "assign" || !strings.Contains(ansi.Strip(opened.boardView().Content), "must join") {
						t.Fatal("assign button did not explain membership")
					}
					continue
				}
				if opened.board.action != "handoff" || !strings.Contains(opened.board.prompt, "work assignment --project 'project' --assignment 'patch'") {
					t.Fatal("instructions did not target the selected assignment")
				}
				for _, private := range []string{"private-login", "sender-profile", "private brief"} {
					if strings.Contains(opened.board.prompt, private) {
						t.Fatalf("instructions exposed %q", private)
					}
				}
				next, cmd = opened.updateBoard(tea.KeyPressMsg{Text: "c"})
				opened = next.(model)
				if cmd == nil || fmt.Sprint(cmd()) != opened.board.prompt || !opened.board.copyRequested {
					t.Fatal("copy did not send the complete prompt to the terminal clipboard")
				}
				next, _ = opened.updateBoard(tea.KeyPressMsg{Code: tea.KeyEnd})
				opened = next.(model)
				if (len(opened.boardPromptRows()) > max(1, opened.bodyHeight()-3) && opened.board.scroll == 0) || !strings.Contains(ansi.Strip(opened.boardView().Content), "Lead acceptance") {
					t.Fatal("prompt could not scroll to its final instructions")
				}
				next, _ = opened.updateBoard(tea.KeyPressMsg{Code: tea.KeyEscape})
				if next.(model).board.action != "" {
					t.Fatal("prompt did not close")
				}
			}
		})
	}
}

func TestWorkBoardContributorCannotReassign(t *testing.T) {
	m := model{client: api.NewClient("https://maruvo.example"), user: api.User{ID: "42"}, width: 80, height: 24, board: workBoard{open: true, project: api.WorkProject{ID: "project", OwnerID: 1, Assignments: []api.WorkAssignment{{ID: "patch", Assignee: 42}}}}}
	for _, hit := range m.boardLayout().hits {
		if hit.action == "w" {
			t.Fatal("contributor was offered reassignment")
		}
	}
	next, cmd := m.updateBoard(tea.KeyPressMsg{Text: "w"})
	if next.(model).board.err == nil || next.(model).board.action != "" || cmd != nil {
		t.Fatal("contributor could open or execute assignment management")
	}
	next, cmd = m.updateBoard(tea.KeyPressMsg{Text: "q"})
	if next.(model).board.action != "handoff" || cmd != nil {
		t.Fatal("contributor could not get instructions for their own assignment")
	}
	m.user.ID = "1"
	m.board.project.Assignments[0].ChildPost = 17
	if m.boardCanAssign() {
		t.Fatal("paid child was offered reassignment away from its directed worker")
	}
}
func TestWorkBoardShowsPinnedEvidenceAndTakeover(t *testing.T) {
	m := model{token: "operator", user: api.User{ID: "1"}, width: 80, height: 30, board: workBoard{open: true, detail: true, project: api.WorkProject{ID: "project", Name: "Bug fix", Status: "active", Assignments: []api.WorkAssignment{{ID: "verify", Title: "Independent verification", State: "blocked", Blocker: "compiler missing", NextAction: "restore compiler", Contributions: []api.WorkContribution{{ID: "evidence-id", Status: "submitted", Inputs: []string{"exact-patch"}, Evidence: []api.WorkEvidence{{Command: "go test ./...", ExitCode: 0, Result: "PASS", Environment: "other runtime"}}}}}}}}}
	view := ansi.Strip(m.boardView().Content)
	for _, text := range []string{"Independent verification", "compiler missing", "restore compiler", "exact-patch", "Reported check", "other runtime"} {
		if !strings.Contains(view, text) {
			t.Fatalf("work board omitted %q", text)
		}
	}
	m.board.detail = false
	next, _ := m.updateBoard(tea.KeyPressMsg{Text: "t"})
	m = next.(model)
	if m.board.action != "control" {
		t.Fatal("board takeover unavailable")
	}
	m.board.action = ""
	next, _ = m.updateBoard(tea.KeyPressMsg{Text: "p"})
	m = next.(model)
	if m.board.err == nil {
		t.Fatal("personal project opened a nonexistent payment")
	}
}
