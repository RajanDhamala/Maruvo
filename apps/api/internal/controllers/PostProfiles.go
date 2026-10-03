package controller

import (
	"context"
	"net/http"

	db "github.com/rajandhamala/Maruvo/db/sqlc"
)

type publicUser struct {
	ID          int64  `json:"id"`
	Username    string `json:"username"`
	Avatar      string `json:"avatar"`
	GitHubLogin string `json:"github_login,omitempty"`
	GitHubURL   string `json:"github_url,omitempty"`
}

type publicPost struct {
	db.Post
	Poster *publicUser `json:"poster"`
	Worker *publicUser `json:"worker"`
}

func (ctrl *Controller) postProfiles(ctx context.Context, posts []db.Post) ([]publicPost, error) {
	ids := []int64{}
	seen := map[int64]bool{}

	for _, post := range posts {
		for _, id := range []int64{post.UserID, post.AcceptedBy.Int64} {
			if id > 0 && !seen[id] {
				ids = append(ids, id)
				seen[id] = true
			}
		}
	}

	profiles := map[int64]*publicUser{}

	if len(ids) > 0 {
		users, err := ctrl.queries.PublicUsers(ctx, ids)
		if err != nil {
			return nil, err
		}

		for _, user := range users {
			profiles[user.ID] = &publicUser{ID: user.ID, Username: user.Username, Avatar: user.Avatar.String,
				GitHubLogin: user.GithubLogin, GitHubURL: githubProfileURL(user.GithubLogin)}
		}
	}

	result := make([]publicPost, 0, len(posts))
	for _, post := range posts {
		result = append(
			result,
			publicPost{Post: post, Poster: profiles[post.UserID], Worker: profiles[post.AcceptedBy.Int64]},
		)
	}

	return result, nil
}

func (ctrl *Controller) writePost(
	w http.ResponseWriter,
	r *http.Request,
	status int,
	post db.Post,
	fields map[string]any,
) {
	posts, err := ctrl.postProfiles(r.Context(), []db.Post{post})
	if err != nil {
		postJSON(w, 503, map[string]string{"error": "task profiles unavailable"})
		return
	}

	if fields == nil {
		fields = map[string]any{}
	}

	fields["post"] = posts[0]
	postJSON(w, status, fields)
}

func (ctrl *Controller) writePosts(w http.ResponseWriter, r *http.Request, posts []db.Post) {
	views, err := ctrl.postProfiles(r.Context(), posts)
	if err != nil {
		postJSON(w, 503, map[string]string{"error": "task profiles unavailable"})
		return
	}

	postJSON(w, 200, map[string]any{"posts": views})
}
