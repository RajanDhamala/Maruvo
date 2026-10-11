package tui

import (
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/rajandhamala/Maruvo/cli/internal/api"
)

func TestTaskDetailControlsStayVisibleWhilePaging(t *testing.T) {
	worker := int64(8)
	fundBy, deliverBy := time.Now().Add(time.Hour), time.Now().Add(72*time.Hour)
	for _, size := range [][2]int{{48, 16}, {60, 20}, {80, 24}, {120, 36}, {168, 48}} {
		m := dashboardFixture()
		m.width, m.height, m.screen, m.own = size[0], size[1], detailScreen, true
		m.selected = 0
		m.posts = []api.Post{{ID: 6, UserID: 7, AcceptedBy: &worker, Status: "negotiating",
			Title: "Build a Solana transaction receipt CLI", CostLamports: 70_000_000,
			Poster: &api.PublicUser{ID: 7, GitHubLogin: strings.Repeat("requester", 10)},
			Worker: &api.PublicUser{ID: 8, GitHubLogin: "worker"},
			FundBy: &fundBy, DeliverBy: &deliverBy, ReviewWindowSeconds: 86400,
			Description:     strings.Repeat("Detailed instructions for this task.\n", 60),
			ExpectedOutputs: []string{"final-artifact.txt"}}}
		view := ansi.Strip(m.View().Content)
		for _, hidden := range []string{"MARUVO", "1 Feed", "2 My Tasks", "3 New Task"} {
			if strings.Contains(view, hidden) {
				t.Fatalf("%v: task detail still shows global navigation: %s", size, hidden)
			}
		}
		profile := m.profileArea()
		next, cmd := m.updateMouse(tea.MouseClickMsg{Button: tea.MouseLeft, X: profile.x, Y: 1})
		if cmd != nil || next.(model).profileOpen {
			t.Fatalf("%v: hidden profile control still receives clicks", size)
		}
		if size[1] >= 24 {
			next, cmd = m.updateMouse(tea.MouseClickMsg{Button: tea.MouseLeft, X: 3, Y: 3})
			if cmd != nil || next.(model).screen != detailScreen {
				t.Fatalf("%v: hidden navigation still receives clicks", size)
			}
		}
		for _, key := range []rune{tea.KeyPgDown, tea.KeyEnd, tea.KeyPgUp, tea.KeyHome} {
			next, cmd := m.updatePosts(tea.KeyPressMsg{Code: key})
			m = next.(model)
			if cmd != nil || m.loading || m.fundingConfirm {
				t.Fatal("paging must not prepare or sign payment")
			}
			layout := m.detailHeader()
			if len(layout.rows) >= m.bodyHeight() {
				t.Fatalf("%v: header leaves no scrolling space", size)
			}
			fund := false
			for _, hit := range layout.hits {
				if hit.x+hit.width > m.contentWidth() || hit.y+hit.height > m.bodyHeight() {
					t.Fatalf("%v: action is outside the visible page: %+v", size, hit)
				}
				fund = fund || hit.action == "f"
			}
			if !fund {
				t.Fatalf("%v: scrolling hid funding action", size)
			}
			rows := strings.Split(m.View().Content, "\n")
			if len(rows) != size[1] {
				t.Fatalf("%v: detail page exceeded terminal height", size)
			}
			for _, row := range rows {
				if ansi.StringWidth(row) > size[0] {
					t.Fatalf("%v: detail page exceeded terminal width", size)
				}
			}
			if key == tea.KeyEnd && !strings.Contains(ansi.Strip(m.View().Content), "final-artifact.txt") {
				t.Fatalf("%v: End did not reveal the final output", size)
			}
		}
		if m.scroll != 0 {
			t.Fatal("Home did not return to the task summary")
		}
	}
}

func TestFundingConfirmationCenteredAndClickable(t *testing.T) {
	fundBy, deliverBy := time.Now().Add(time.Hour), time.Now().Add(72*time.Hour)
	for _, size := range [][2]int{{48, 16}, {60, 20}, {80, 24}, {120, 36}, {168, 48}} {
		m := recoveryFixture()
		m.width, m.height, m.fundingConfirm = size[0], size[1], true
		post := &m.posts[0]
		post.Title, post.CostLamports = "Build a Solana transaction receipt CLI", 70_000_000
		post.FundBy, post.DeliverBy, post.ReviewWindowSeconds = &fundBy, &deliverBy, 86400
		m.escrow = api.Escrow{State: "prepared", Network: "devnet", Reviewer: strings.Repeat("r", 44),
			FeeLamports: 5000, StorageLamports: 1432560}
		layout := m.postsLayout()
		if len(layout.rows) > m.bodyHeight() {
			t.Fatalf("%v: funding confirmation exceeds the viewport", size)
		}
		top := 0
		for top < len(layout.rows) && strings.TrimSpace(ansi.Strip(layout.rows[top])) == "" {
			top++
		}
		bottom := m.bodyHeight() - len(layout.rows)
		if top-bottom > 1 || bottom-top > 1 {
			t.Fatalf("%v: funding panel is not vertically centered: %d/%d", size, top, bottom)
		}
		left, right := m.contentX(), size[0]-m.contentX()-m.contentWidth()
		if left-right > 1 || right-left > 1 {
			t.Fatalf("%v: funding panel is not horizontally centered", size)
		}
		view := ansi.Strip(m.View().Content)
		for _, term := range []string{"0.07 SOL", "5000 lamports", "1432560 lamports", "Fund by", "Deliver by", "24h", "Reviewer"} {
			if !strings.Contains(view, term) {
				t.Fatalf("%v: funding confirmation hid %s", size, term)
			}
		}
		rows := strings.Split(m.View().Content, "\n")
		if len(rows) != size[1] {
			t.Fatalf("%v: funding frame exceeded terminal height", size)
		}
		for _, row := range rows {
			if ansi.StringWidth(row) > size[0] {
				t.Fatalf("%v: funding frame exceeded terminal width", size)
			}
		}
		if len(layout.hits) != 2 {
			t.Fatalf("%v: funding controls are missing", size)
		}
		for _, hit := range layout.hits {
			if hit.x+hit.width > m.contentWidth() || hit.y+hit.height > m.bodyHeight() {
				t.Fatalf("%v: funding control outside the panel: %+v", size, hit)
			}
			next, cmd := m.updateMouse(tea.MouseClickMsg{Button: tea.MouseLeft,
				X: m.contentX() + hit.x, Y: m.bodyStart() + hit.y})
			result := next.(model)
			if hit.action == "cancel" && (cmd != nil || result.fundingConfirm || result.loading) {
				t.Fatalf("%v: Cancel must dismiss without signing", size)
			}
			if hit.action == "fund-confirm" && (cmd == nil || result.fundingConfirm || !result.loading) {
				t.Fatalf("%v: Sign must reach the existing signing handler", size)
			}
		}
	}
}
