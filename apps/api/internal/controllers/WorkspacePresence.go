package controller

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"
)

func presenceKey(post, user int64) string {
	return fmt.Sprintf("maruvo:workspace:%d:presence:%d", post, user)
}

func (c *Controller) touchPresence(ctx context.Context, post, user int64, connection string) error {
	key := presenceKey(post, user)
	now := time.Now()
	pipe := c.redis.TxPipeline()
	pipe.ZRemRangeByScore(ctx, key, "-inf", strconv.FormatInt(now.UnixMilli(), 10))
	pipe.ZAdd(ctx, key, redis.Z{Score: float64(now.Add(pongWait).UnixMilli()), Member: connection})
	pipe.Expire(ctx, key, 2*pongWait)
	_, err := pipe.Exec(ctx)
	return err
}

func (c *Controller) WorkspacePresence(w http.ResponseWriter, r *http.Request) {
	user, ok := postUserID(w, r)
	if !ok {
		return
	}
	id, ok := workspacePostID(w, r)
	if !ok {
		return
	}
	post, err := c.queries.GetPost(r.Context(), id)
	if !workspaceAccess(w, r, c.queries, post, err, user) {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	pipe := c.redis.Pipeline()
	cutoff := "(" + strconv.FormatInt(time.Now().UnixMilli(), 10)
	requester := pipe.ZCount(ctx, presenceKey(id, post.UserID), cutoff, "+inf")
	worker := pipe.ZCount(ctx, presenceKey(id, post.AcceptedBy.Int64), cutoff, "+inf")
	if _, err := pipe.Exec(ctx); err != nil {
		postJSON(w, 503, map[string]string{"error": "workspace presence unavailable"})
		return
	}
	postJSON(w, 200, map[string]bool{"requester_online": requester.Val() > 0, "worker_online": worker.Val() > 0})
}
