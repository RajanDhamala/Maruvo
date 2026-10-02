package controller

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gorilla/websocket"
	db "github.com/rajandhamala/Maruvo/db/sqlc"
	"github.com/rajandhamala/Maruvo/internal/utils"
)

const (
	pongWait   = 65 * time.Second
	pingPeriod = 30 * time.Second
	writeWait  = 10 * time.Second
)

var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
}

type WsResponse struct {
	Event string `json:"event"`
	Data  any    `json:"data"`
}

func (c *Controller) WsHandler(w http.ResponseWriter, r *http.Request) {
	userID, ok := postUserID(w, r)
	if !ok {
		return
	}

	id, err := strconv.ParseInt(r.URL.Query().Get("post_id"), 10, 64)
	if err != nil || id <= 0 {
		postJSON(w, 400, map[string]string{"error": "invalid post ID"})
		return
	}

	after := int64(0)
	if value := r.URL.Query().Get("after"); value != "" {
		after, err = strconv.ParseInt(value, 10, 64)
		if err != nil || after < 0 {
			postJSON(w, 400, map[string]string{"error": "invalid event cursor"})
			return
		}
	}

	post, err := c.queries.GetPost(r.Context(), id)
	if !workspaceAccess(w, r, c.queries, post, err, userID) {
		return
	}

	workspace, err := c.queries.GetWorkspace(r.Context(), id)
	if err != nil {
		workspaceError(w, err)
		return
	}

	if after > workspace.LastEventID {
		postJSON(w, 400, map[string]string{"error": "event cursor is ahead of this workspace"})
		return
	}

	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer conn.Close()

	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()

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

	write := func(event string, data any) error {
		conn.SetWriteDeadline(time.Now().Add(writeWait))
		return conn.WriteJSON(WsResponse{Event: event, Data: data})
	}
	if write("connected", map[string]int64{"post_id": id, "after": after}) != nil {
		return
	}

	poll := time.NewTicker(time.Second)
	ping := time.NewTicker(pingPeriod)

	defer poll.Stop()
	defer ping.Stop()

	for {
		token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		if _, err := utils.VerifyUserToken(token); err != nil {
			conn.WriteControl(
				websocket.CloseMessage,
				websocket.FormatCloseMessage(websocket.ClosePolicyViolation,
					"session expired"),
				time.Now().Add(writeWait),
			)

			return
		}

		queryCtx, stop := context.WithTimeout(ctx, 5*time.Second)
		events, err := c.queries.ListWorkspaceEvents(
			queryCtx,
			db.ListWorkspaceEventsParams{PostID: id, ID: after},
		)

		stop()

		if err != nil {
			return
		}

		for _, event := range events {
			if write("workspace.event", event) != nil {
				return
			}

			after = event.ID
		}

		if len(events) == 100 {
			continue
		}

		select {
		case <-ctx.Done():
			return
		case <-poll.C:
		case <-ping.C:
			token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
			if _, err := utils.VerifyUserToken(token); err != nil {
				conn.WriteControl(
					websocket.CloseMessage,
					websocket.FormatCloseMessage(websocket.ClosePolicyViolation,
						"session expired"),
					time.Now().Add(writeWait),
				)

				return
			}

			if conn.WriteControl(websocket.PingMessage, nil, time.Now().Add(writeWait)) != nil {
				return
			}
		}
	}
}
