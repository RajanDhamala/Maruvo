package tui

import (
	"errors"
	"net/http"
	"slices"
	"sync"

	tea "charm.land/bubbletea/v2"
	"github.com/rajandhamala/Maruvo/cli/internal/api"
)

type dashboardResult struct {
	feed, mine []api.Post
	generation uint64
	err        error
}

func (m model) fetchDashboard() tea.Cmd {
	return func() tea.Msg {
		results := make([]struct {
			posts []api.Post
			err   error
		}, len(levels)+1)

		var wait sync.WaitGroup
		for i := range results {
			wait.Add(1)
			go func() {
				defer wait.Done()

				if i == len(levels) {
					results[i].posts, results[i].err = m.client.OwnPosts(m.ctx, m.token)
				} else {
					results[i].posts, results[i].err = m.client.Feed(m.ctx, m.token, levels[i])
				}
			}()
		}

		wait.Wait()

		result := dashboardResult{generation: m.dashboard.generation}
		seen := make(map[int64]bool)

		for i, response := range results {
			if response.err != nil {
				var failure *api.Error

				unauthorized := errors.As(response.err, &failure) &&
					failure.StatusCode == http.StatusUnauthorized
				if result.err == nil || unauthorized {
					result.err = response.err
				}
			}

			if i == len(levels) {
				result.mine = response.posts
				continue
			}

			for _, post := range response.posts {
				if !seen[post.ID] {
					result.feed = append(result.feed, post)
					seen[post.ID] = true
				}
			}
		}

		slices.SortStableFunc(result.feed, func(a, b api.Post) int {
			return b.CreatedAt.Compare(a.CreatedAt)
		})
		slices.SortStableFunc(result.mine, func(a, b api.Post) int {
			return postTime(b).Compare(postTime(a))
		})

		return result
	}
}
