package controller

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/gorilla/websocket"
	"github.com/redis/go-redis/v9"
	"net/http"
	"time"
)

func workChannel(user int64) string   { return fmt.Sprintf("maruvo:work-invites:%d", user) }
func workInviteKey(user int64) string { return workChannel(user) + ":pending" }

func (c *Controller) WorkInvites(w http.ResponseWriter, r *http.Request) {
	user, ok := postUserID(w, r)
	if !ok {
		return
	}
	if requestAgent(r.Context()) != nil {
		postJSON(w, 403, map[string]string{"error": "human session required"})
		return
	}
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	sub := c.redis.Subscribe(ctx, workChannel(user))
	defer sub.Close()
	if _, err := sub.Receive(ctx); err != nil {
		postJSON(w, 503, map[string]string{"error": "invitation service unavailable"})
		return
	}
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer conn.Close()
	conn.SetReadLimit(4096)
	conn.SetReadDeadline(time.Now().Add(pongWait))
	conn.SetPongHandler(func(string) error { return conn.SetReadDeadline(time.Now().Add(pongWait)) })
	go func() {
		defer cancel()
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				return
			}
		}
	}()
	write := func(data string) error {
		conn.SetWriteDeadline(time.Now().Add(writeWait))
		var state struct {
			Status string `json:"status"`
		}
		_ = json.Unmarshal([]byte(data), &state)
		event := "work.invite"
		if state.Status != "" {
			event = "work.status"
		}
		return conn.WriteJSON(WsResponse{Event: event, Data: json.RawMessage(data)})
	}
	if pending, err := c.redis.Get(ctx, workInviteKey(user)).Result(); err == nil {
		var invitation struct {
			PostID int64  `json:"post_id"`
			From   int64  `json:"from"`
			Nonce  string `json:"nonce"`
		}
		if json.Unmarshal([]byte(pending), &invitation) == nil && c.redis.Get(ctx, readyKey(invitation.PostID, invitation.From)).Val() == invitation.Nonce {
			if write(pending) != nil {
				return
			}
		}
	}
	ticker := time.NewTicker(pingPeriod)
	defer ticker.Stop()
	messages := sub.Channel()
	for {
		select {
		case <-ctx.Done():
			return
		case message, ok := <-messages:
			if !ok {
				return
			}
			if c.checkSession(ctx, r) != nil {
				return
			}
			if write(message.Payload) != nil {
				return
			}
		case <-ticker.C:
			if c.checkSession(ctx, r) != nil {
				return
			}
			if conn.WriteControl(websocket.PingMessage, nil, time.Now().Add(writeWait)) != nil {
				return
			}
		}
	}
}

func (c *Controller) clearWorkInvite(ctx context.Context, user int64, nonce string) {
	_ = redis.NewScript(`local value=redis.call('GET',KEYS[1]);if value and cjson.decode(value).nonce==ARGV[1] then redis.call('DEL',KEYS[1]) end;return 1`).Run(ctx, c.redis, []string{workInviteKey(user)}, nonce).Err()
}
