package tui

import (
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/rajandhamala/Maruvo/cli/internal/api"
)

var taskFilters = []string{"All", "Created", "Accepted"}

var taskRoles = []string{"", "Created", "Accepted", "Reviewer"}

func (m model) taskRole(post api.Post) int {
	if m.isPoster(post) {
		return 1
	}

	if m.isWorker(post) {
		return 2
	}

	return 3
}

func (m model) listIndices() []int {
	indices := make([]int, 0, len(m.posts))
	for i, post := range m.posts {
		if !m.own && m.dashboard.ready && !m.dashboard.all && post.Level != levels[m.level] {
			continue
		}

		query := strings.ToLower(strings.TrimSpace(m.dashboard.search.value))

		text := strings.ToLower(post.Title + " " + post.Level + " " + taskStatus(post) + " " +
			participantLabel(post.Poster, post.UserID))
		if post.AcceptedBy != nil {
			text += " " + strings.ToLower(participantLabel(post.Worker, *post.AcceptedBy))
		}

		if query != "" && !strings.Contains(text, query) {
			continue
		}

		if !m.own || m.taskFilter == 0 || m.taskRole(post) == m.taskFilter {
			indices = append(indices, i)
		}
	}

	return indices
}

func (m *model) selectVisiblePost() {
	indices := m.listIndices()
	if len(indices) > 0 && !slices.Contains(indices, m.selected) {
		m.selected = indices[0]
	}
}

func (m *model) movePost(direction int) {
	indices := m.listIndices()
	if len(indices) == 0 {
		return
	}

	position := max(0, slices.Index(indices, m.selected))
	m.selected = indices[min(len(indices)-1, max(0, position+direction))]
}

func (m model) filterTasks(filter int) model {
	m.taskFilter = filter
	m.selected = 0
	m.selectVisiblePost()

	return m
}

func taskBudget(lamports int64) string {
	fraction := strings.TrimRight(fmt.Sprintf("%09d", lamports%1_000_000_000), "0")

	amount := strconv.FormatInt(lamports/1_000_000_000, 10)
	if fraction != "" {
		amount += "." + fraction
	}

	return amount + " SOL"
}

func participantLabel(profile *api.PublicUser, id int64) string {
	if profile != nil {
		if profile.GitHubLogin != "" {
			return "@" + plain(profile.GitHubLogin)
		}

		if profile.Username != "" {
			return plain(profile.Username) + fmt.Sprintf(" (#%d)", id)
		}
	}

	return fmt.Sprintf("User #%d", id)
}
