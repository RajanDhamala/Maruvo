package tui

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/rajandhamala/Maruvo/cli/internal/api"
)

func recoveryFixture() model {
	m := dashboardFixture()
	worker := int64(8)
	m.screen, m.selected = detailScreen, 0
	m.posts[0].UserID, m.posts[0].AcceptedBy, m.posts[0].Status = 7, &worker, "negotiating"
	m.posts[0].EndTime = time.Now().Add(time.Hour)
	m.escrow.State = "unfunded"
	m.homeInput = textField{value: "Keep my draft", cursor: 13, limit: 2000}

	return m
}

func TestTaskRecoveryConfirmationAndRequest(t *testing.T) {
	for _, action := range []string{"cancel", "reopen"} {
		m := recoveryFixture()
		oldID := m.posts[0].ID
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/posts/recover" || r.Header.Get("Authorization") != "Bearer session" {
				t.Error("recovery must use the authenticated recovery endpoint")
			}

			var payload struct {
				ID      int64     `json:"id"`
				Action  string    `json:"action"`
				EndTime time.Time `json:"end_time"`
			}
			if err := json.NewDecoder(r.Body).
				Decode(&payload); err != nil || payload.ID != oldID ||
				payload.Action != action {
				t.Errorf("incorrect recovery payload: %+v, %v", payload, err)
			}

			post := m.posts[0]
			post.Status = "cancelled"

			if action == "reopen" {
				if !payload.EndTime.After(time.Now()) {
					t.Error("reopening must send the visible future acceptance cutoff")
				}

				post.ID, post.Status, post.AcceptedBy = 99, "open", nil
			}

			json.NewEncoder(w).Encode(map[string]api.Post{"post": post})
		}))
		t.Cleanup(server.Close)
		m.client = api.NewClient(server.URL)

		key := 'x'
		if action == "reopen" {
			key = 'o'
		}

		next, cmd := m.Update(tea.KeyPressMsg{Code: key, Text: string(key)})
		if cmd != nil || next.(model).recovering != action {
			t.Fatal("recovery must ask for confirmation before sending a request")
		}

		next, _ = next.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
		if next.(model).recovering != "" || next.(model).posts[0].Status != "negotiating" {
			t.Fatal("canceling confirmation must leave the task unchanged")
		}

		next, _ = next.Update(tea.KeyPressMsg{Code: key, Text: string(key)})

		next, cmd = next.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
		if cmd == nil {
			t.Fatal("confirmed recovery must call the API")
		}

		next, _ = next.Update(cmd())

		result := next.(model)
		if result.recovering != "" || result.homeInput.value != "Keep my draft" ||
			result.dashboard.ready {
			t.Fatal("successful recovery must refresh task state while retaining the draft")
		}

		if action == "reopen" && (result.posts[0].ID != 99 || result.posts[0].AcceptedBy != nil ||
			result.escrow.State != "unfunded") {
			t.Fatal("reopening must display the new, unaccepted task with no old escrow")
		}
	}
}

func TestTaskRecoveryGuardsAndFailedRequests(t *testing.T) {
	for _, state := range []string{"confirmed", "released", "refunded"} {
		m := recoveryFixture()
		m.escrow.State = state

		next, cmd := m.beginRecovery("o")
		if cmd != nil || next.(model).recovering != "" {
			t.Fatal("funded tasks must use reviewer settlement instead of recovery")
		}
	}

	m := recoveryFixture()
	m.user.ID = "8"

	next, _ := m.beginRecovery("x")
	if next.(model).recovering != "" {
		t.Fatal("only the requester can open recovery controls")
	}

	m = recoveryFixture()
	next, _ = m.beginRecovery("o")

	next, _ = next.Update(postChanged{recovered: "reopen", err: errors.New("funding still active")})
	if next.(model).recovering != "reopen" || next.(model).posts[0].Status != "negotiating" ||
		next.(model).err == nil {
		t.Fatal("failed recovery must retain the confirmation and original task")
	}
}

func TestViewingReopenedTaskRefreshesFunding(t *testing.T) {
	m := recoveryFixture()
	reopenedID := int64(99)
	m.posts[0].Status, m.posts[0].ReopenedAs = "cancelled", &reopenedID
	m.escrow.State = "expired"

	reopened := m.posts[0]
	reopened.ID, reopened.Status, reopened.ReopenedAs = reopenedID, "in_progress", nil

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer session" {
			t.Error("viewing the successor must remain authenticated")
		}

		switch r.URL.Path {
		case "/posts/recover":
			json.NewEncoder(w).Encode(map[string]api.Post{"post": reopened})
		case "/posts/info":
			var payload struct {
				ID int64 `json:"id"`
			}
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil || payload.ID != reopenedID {
				t.Error("funding must be refreshed for the successor, not the old task")
			}

			json.NewEncoder(w).Encode(api.PostInfo{Post: reopened, Escrow: api.Escrow{State: "confirmed"}})
		default:
			t.Errorf("unexpected request: %s", r.URL.Path)
		}
	}))
	defer server.Close()

	m.client = api.NewClient(server.URL)

	next, cmd := m.beginRecovery("o")
	if cmd == nil {
		t.Fatal("viewing an existing successor must fetch it without creating another task")
	}

	next, cmd = next.Update(cmd())
	if cmd == nil || !next.(model).loading {
		t.Fatal("the successor's current funding state must be loaded")
	}

	next, _ = next.Update(cmd())

	result := next.(model)
	if result.loading || result.err != nil || result.posts[0].ID != reopenedID ||
		result.escrow.State != "confirmed" || result.canRecover(result.posts[0]) {
		t.Fatal("a funded successor must retain its assignment and use reviewer settlement")
	}
}

func TestTaskRecoveryDateMouseAndShortLayout(t *testing.T) {
	for _, size := range [][2]int{{48, 16}, {80, 24}, {120, 36}} {
		m := recoveryFixture()
		m.width, m.height, m.escrow.State = size[0], size[1], "prepared"
		m.posts[0].EndTime = time.Now().Add(-time.Hour)
		m.form = newPostForm()
		oldDate := m.form.fields[2].value
		next, _ := m.beginRecovery("o")

		m = next.(model)
		if !m.recoveryDeadline.After(time.Now()) {
			t.Fatal("an expired cutoff must offer an editable future date")
		}

		l := m.detailLayout()
		for _, hit := range l.hits {
			if hit.y >= m.bodyHeight() || hit.x+hit.width > m.contentWidth() {
				t.Fatalf("%v: recovery control is inaccessible: %+v", size, hit)
			}

			if hit.action == "recovery-deadline" {
				next, _ = m.Update(tea.MouseClickMsg{Button: tea.MouseLeft,
					X: m.contentX() + hit.x, Y: m.bodyStart() + hit.y})
				if !next.(model).picker.open {
					t.Fatal("clicking the cutoff must open the existing date picker")
				}

				next, _ = next.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
				if next.(model).picker.open || next.(model).form.fields[2].value != oldDate {
					t.Fatal("applying the recovery cutoff must preserve the creation draft")
				}
			}
		}

		if !strings.Contains(ansi.Strip(m.View().Content), "Reopen as new") {
			t.Fatal("recovery confirmation must retain its action in short terminals")
		}
	}
}
