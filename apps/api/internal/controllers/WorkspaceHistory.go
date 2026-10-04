package controller

import (
	"net/http"
	"slices"
	"strconv"

	db "github.com/rajandhamala/Maruvo/db/sqlc"
)

func (c *Controller) WorkspaceHistory(w http.ResponseWriter, r *http.Request) {
	userID, ok := postUserID(w, r)
	if !ok {
		return
	}

	id, ok := workspacePostID(w, r)
	if !ok {
		return
	}

	before, limit := int64(0), int64(50)

	var err error
	if value := r.URL.Query().Get("before"); value != "" {
		before, err = strconv.ParseInt(value, 10, 64)
		if err != nil || before < 0 {
			postJSON(w, 400, map[string]string{"error": "before must be a nonnegative archived event ID"})
			return
		}
	}

	if value := r.URL.Query().Get("limit"); value != "" {
		limit, err = strconv.ParseInt(value, 10, 64)
		if err != nil || limit < 1 || limit > 100 {
			postJSON(w, 400, map[string]string{"error": "limit must be between 1 and 100"})
			return
		}
	}

	post, err := c.queries.GetPost(r.Context(), id)
	if !workspaceAccess(w, r, c.queries, post, err, userID) {
		return
	}

	events, err := c.queries.ListWorkspaceHistory(r.Context(), db.ListWorkspaceHistoryParams{
		PostID: id, BeforeID: before, PageSize: int32(limit + 1),
	})
	if err != nil {
		workspaceError(w, err)
		return
	}

	hasMore := len(events) > int(limit)
	nextBefore := int64(0)

	if hasMore {
		events = events[:limit]
		nextBefore = events[len(events)-1].ID
	}

	if events == nil {
		events = []db.WorkspaceEvent{}
	}

	slices.Reverse(events)
	postJSON(w, 200, map[string]any{
		"post_id": id, "events": events, "has_more": hasMore, "next_before": nextBefore,
	})
}
