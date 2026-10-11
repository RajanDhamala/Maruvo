package controller

import (
	"context"
	"encoding/json"
	"github.com/redis/go-redis/v9"
	"time"
)

func validWorkStatus(status string) bool {
	switch status {
	case "", "ready", "working", "waiting_for_approval", "paused", "listening":
		return true
	}
	return false
}

var workStatusScript = redis.NewScript(`
if redis.call('GET',KEYS[1]) ~= ARGV[1] then return 0 end
local previous=redis.call('GET',KEYS[2])
redis.call('SET',KEYS[2],ARGV[2],'EX',30)
if previous ~= ARGV[2] then redis.call('PUBLISH',ARGV[3],ARGV[2]) end
return 1
`)

func (c *Controller) reportWorkStatus(ctx context.Context, post, user, peer int64, nonce, status, detail string) error {
	event, err := json.Marshal(map[string]any{"post_id": post, "nonce": nonce, "status": status, "detail": detail})
	if err != nil {
		return err
	}
	return workStatusScript.Run(ctx, c.redis, []string{readyKey(post, user), readyKey(post, user) + ":status"}, nonce, string(event), workChannel(peer)).Err()
}

func (c *Controller) workStatus(ctx context.Context, post, user int64, nonce string) (string, string) {
	if nonce == "" {
		return "", ""
	}
	ctx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	value, err := c.redis.Get(ctx, readyKey(post, user)+":status").Result()
	if err != nil {
		return "", ""
	}
	var state struct {
		Nonce  string `json:"nonce"`
		Status string `json:"status"`
		Detail string `json:"detail"`
	}
	if json.Unmarshal([]byte(value), &state) != nil || state.Nonce != nonce {
		return "", ""
	}
	return state.Status, state.Detail
}
