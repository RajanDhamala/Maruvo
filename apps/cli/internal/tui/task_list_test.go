package tui

import (
	"context"
	"slices"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/rajandhamala/Maruvo/cli/internal/api"
)

func TestTaskFilterNavigation(t *testing.T) {
	worker := int64(7)
	m := model{
		ctx: context.Background(), token: "token", user: api.User{ID: "7"},
		own: true, screen: myPostsScreen, width: 80, height: 24,
		posts: []api.Post{
			{ID: 10, UserID: 7},
			{ID: 20, UserID: 8, AcceptedBy: &worker},
			{ID: 30, UserID: 9},
			{ID: 40, UserID: 8, AcceptedBy: &worker},
		},
	}

	m = m.filterTasks(2)
	if !slices.Equal(m.listIndices(), []int{1, 3}) || m.selected != 1 {
		t.Fatal("accepted filter must preserve the original post indices")
	}

	next, _ := m.updatePosts(tea.KeyPressMsg{Code: tea.KeyDown})

	m = next.(model)
	if m.selected != 3 {
		t.Fatal("down must skip posts outside the selected role")
	}

	next, _ = m.updatePosts(tea.KeyPressMsg{Code: tea.KeyDown})
	if next.(model).selected != 3 {
		t.Fatal("down must stay at the last visible task")
	}

	clicked := false

	for _, hit := range m.listLayout().hits {
		if hit.action == "post" && hit.index == 3 {
			clicked = true

			next, cmd := m.updateMouse(tea.MouseClickMsg{
				X: m.contentX() + hit.x, Y: m.bodyStart() + hit.y, Button: tea.MouseLeft,
			})
			if next.(model).screen != detailScreen || next.(model).posts[next.(model).selected].ID != 40 ||
				cmd == nil {
				t.Fatal("click must open the task displayed in the filtered list")
			}
		}
	}

	if !clicked {
		t.Fatal("selected task must have a mouse target")
	}

	next, _ = m.Update(
		dashboardResult{mine: []api.Post{{ID: 50, UserID: 7}, {ID: 60, UserID: 8, AcceptedBy: &worker}}},
	)

	m = next.(model)
	if m.selected != 1 {
		t.Fatal("refresh must select a visible task")
	}

	m = m.filterTasks(1)

	next, _ = m.updatePosts(tea.KeyPressMsg{Code: tea.KeyRight})
	if next.(model).taskFilter != 2 || next.(model).selected != 1 {
		t.Fatal("keyboard role navigation must update the selected task")
	}

	m.posts = nil

	next, cmd := m.updatePosts(tea.KeyPressMsg{Code: tea.KeyEnter})
	if cmd != nil || next.(model).screen != myPostsScreen {
		t.Fatal("enter on an empty filtered list must not open a task")
	}
}
