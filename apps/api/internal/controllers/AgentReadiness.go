package controller

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

var readinessScript = redis.NewScript(`
if ARGV[1] == 'ready' then
 if redis.call('EXISTS',KEYS[1] .. ':cancelled:' .. ARGV[2]) == 1 then return redis.error_reply('session cancelled') end
 if ARGV[3] and ARGV[3] ~= '' and redis.call('GET', KEYS[2]) ~= ARGV[3] then return redis.error_reply('invitation expired') end
 local current = redis.call('GET', KEYS[1])
 if current and current ~= ARGV[2] then return redis.error_reply('another session is ready for this account') end
 redis.call('SET', KEYS[1], ARGV[2], 'EX', 30)
elseif ARGV[1] == 'stop' then
 redis.call('SET',KEYS[1] .. ':cancelled:' .. ARGV[2], '1', 'EX', 60)
 if redis.call('GET',KEYS[1]) == ARGV[2] then redis.call('DEL', KEYS[1]) end
end
return {redis.call('GET', KEYS[1]) or '', redis.call('GET', KEYS[2]) or ''}
`)

func readyKey(post, user int64) string {
	return fmt.Sprintf("maruvo:workspace:%d:ready:%d", post, user)
}

func (c *Controller) AgentReadiness(w http.ResponseWriter, r *http.Request) {
	user, ok := postUserID(w, r)
	if !ok {
		return
	}
	id, ok := workspacePostID(w, r)
	if !ok {
		return
	}
	if requestAgent(r.Context()) != nil {
		postJSON(w, 403, map[string]string{"error": "only the human account can approve readiness"})
		return
	}
	post, err := c.queries.GetPost(r.Context(), id)
	if !workspaceAccess(w, r, c.queries, post, err, user) {
		return
	}
	if !post.AcceptedBy.Valid || (user != post.UserID && user != post.AcceptedBy.Int64) {
		postJSON(w, 403, map[string]string{"error": "only task participants can coordinate agents"})
		return
	}
	state, nonce := "read", ""
	invite, decline, target := false, false, ""
	status, detail := "", ""
	if r.Method == http.MethodPost {
		var input struct {
			Ready   bool   `json:"ready"`
			Nonce   string `json:"nonce"`
			Invite  bool   `json:"invite"`
			Decline bool   `json:"decline"`
			Target  string `json:"target"`
			Status  string `json:"status"`
			Detail  string `json:"detail"`
		}
		if !decodeOffer(w, r, &input) {
			return
		}
		if len(input.Nonce) < 32 || len(input.Nonce) > 128 {
			postJSON(w, 400, map[string]string{"error": "invalid readiness session"})
			return
		}
		status, detail = input.Status, input.Detail
		if !validWorkStatus(status) || len(detail) > 240 {
			postJSON(w, 400, map[string]string{"error": "invalid collaboration status"})
			return
		}
		nonce = input.Nonce
		invite, decline, target = input.Invite, input.Decline, input.Target
		if len(target) > 128 {
			postJSON(w, 400, map[string]string{"error": "invalid invitation"})
			return
		}
		state = "stop"
		if input.Ready {
			escrow, err := c.queries.GetPostEscrow(r.Context(), id)
			if err != nil || escrow.State != "confirmed" {
				postJSON(w, 409, map[string]string{"error": "confirm escrow funding before starting agents"})
				return
			}
			if post.Status == "completed" || post.Status == "cancelled" {
				postJSON(w, 409, map[string]string{"error": "task is closed"})
				return
			}
			control, err := agentControl(r.Context(), c.queries, id, user)
			if err != nil {
				workspaceError(w, err)
				return
			}
			if control.Mode == "manual" {
				postJSON(w, 409, map[string]string{"error": "agent access is paused"})
				return
			}
			state = "ready"
		}
	}
	peer := post.UserID
	if user == peer {
		peer = post.AcceptedBy.Int64
	}
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	values, err := readinessScript.Run(ctx, c.redis, []string{readyKey(id, user), readyKey(id, peer)}, state, nonce, target).StringSlice()
	if err != nil {
		for _, reason := range []string{"invitation expired", "session cancelled", "another session is ready for this account"} {
			if strings.Contains(err.Error(), reason) {
				postJSON(w, 409, map[string]string{"error": reason})
				return
			}
		}
		postJSON(w, 503, map[string]string{"error": "agent readiness service unavailable; retry when connected"})
		return
	}
	own, other := values[0], values[1]
	if state == "stop" && !decline && own == "" {
		c.clearWorkInvite(ctx, peer, nonce)
		event, _ := json.Marshal(map[string]any{"post_id": id, "cancelled": true, "nonce": nonce})
		_ = c.redis.Publish(ctx, workChannel(peer), string(event)).Err()
	}
	if invite && state == "ready" {
		event, _ := json.Marshal(map[string]any{"post_id": id, "title": post.Title, "from": user, "nonce": nonce})
		if err := c.redis.Set(ctx, workInviteKey(peer), event, 5*time.Minute).Err(); err != nil {
			workspaceError(w, err)
			return
		}
		if err := c.redis.Publish(ctx, workChannel(peer), string(event)).Err(); err != nil {
			workspaceError(w, err)
			return
		}
	}
	if decline && target != "" && other == target {
		c.clearWorkInvite(ctx, user, target)
		event, _ := json.Marshal(map[string]any{"post_id": id, "declined": true, "nonce": target})
		_ = c.redis.Publish(ctx, workChannel(peer), string(event)).Err()
	}
	if state == "ready" && target != "" {
		c.clearWorkInvite(ctx, user, target)
	}

	pair := ""
	if own != "" && other != "" {
		if user == post.UserID {
			pair = own + ":" + other
		} else {
			pair = other + ":" + own
		}
	}
	if state == "ready" && status != "" {
		if err := c.reportWorkStatus(ctx, id, user, peer, nonce, status, detail); err != nil {
			workspaceError(w, err)
			return
		}
	}
	peerStatus, peerDetail := c.workStatus(ctx, id, peer, other)
	postJSON(w, 200, map[string]any{"you_ready": own != "", "peer_ready": other != "", "pair": pair, "peer_status": peerStatus, "peer_detail": peerDetail})
}
