package controller

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/rajandhamala/Maruvo/internal/utils"

	"github.com/gorilla/websocket"
	"github.com/jackc/pgx/v5"
	db "github.com/rajandhamala/Maruvo/db/sqlc"
	"github.com/redis/go-redis/v9"
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

	cursor := r.URL.Query().Get("cursor")
	if cursor != "" && !validStreamCursor(cursor) {
		postJSON(w, 400, map[string]string{"error": "invalid stream cursor"})
		return
	}

	post, err := c.queries.GetPost(r.Context(), id)
	if !workspaceAccess(w, r, c.queries, post, err, userID) {
		return
	}

	if cursor == "" {
		cursor = "0-0"

		if value := r.URL.Query().Get("after"); value != "" && value != "0" {
			after, err := strconv.ParseInt(value, 10, 64)
			if err != nil || after < 0 {
				postJSON(w, 400, map[string]string{"error": "invalid event cursor"})
				return
			}

			saved, err := c.queries.WorkspaceEventStreamID(
				r.Context(),
				db.WorkspaceEventStreamIDParams{PostID: id, ID: after},
			)
			if errors.Is(err, pgx.ErrNoRows) {
				postJSON(w, 400, map[string]string{"error": "unknown event cursor"})
				return
			}

			if err != nil {
				workspaceError(w, err)
				return
			}

			if !saved.Valid {
				postJSON(
					w,
					409,
					map[string]string{"error": "event is not published yet; reload the workspace"},
				)

				return
			}

			cursor = saved.String
		}
	}

	_, latest, err := c.recentStreamEvents(r.Context(), id)
	if err != nil {
		postJSON(w, 503, map[string]string{"error": "workspace stream unavailable"})
		return
	}

	if streamCursorAfter(cursor, latest) {
		postJSON(w, 409, map[string]string{"error": "workspace stream reset; reload the workspace"})
		return
	}

	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer conn.Close()

	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	connection := uuid.NewString()
	human := r.Context().Value(utils.AgentKey) == nil
	touch := func() {
		if human {
			presenceCtx, stop := context.WithTimeout(ctx, 2*time.Second)
			defer stop()
			_ = c.touchPresence(presenceCtx, id, userID, connection)
		}
	}
	touch()
	defer func() {
		if human {
			cleanup, stop := context.WithTimeout(context.Background(), 2*time.Second)
			defer stop()
			_ = c.redis.ZRem(cleanup, presenceKey(id, userID), connection).Err()
		}
	}()

	conn.SetReadLimit(4096)
	conn.SetReadDeadline(time.Now().Add(pongWait))
	conn.SetPongHandler(func(string) error {
		touch()
		return conn.SetReadDeadline(time.Now().Add(pongWait))
	})

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
	if write("connected", map[string]any{"post_id": id, "cursor": cursor}) != nil {
		return
	}

	lastPing := time.Now()

	for ctx.Err() == nil {
		if err := c.checkSession(ctx, r); err != nil {
			conn.WriteControl(websocket.CloseMessage,
				websocket.FormatCloseMessage(websocket.ClosePolicyViolation, "session expired"),
				time.Now().Add(writeWait))

			return
		}

		streams, err := c.redis.XRead(ctx, &redis.XReadArgs{
			Streams: []string{workspaceStreamKey(id), cursor},
			Count:   100,
			Block:   5 * time.Second,
		}).Result()
		if err != nil && !errors.Is(err, redis.Nil) {
			return
		}

		if c.checkSession(ctx, r) != nil {
			return
		}

		for _, stream := range streams {
			for _, message := range stream.Messages {
				event, err := decodeStreamEvent(message)
				if err != nil || write("workspace.event", event) != nil {
					return
				}

				cursor = message.ID
			}
		}

		if time.Since(lastPing) >= pingPeriod {
			if conn.WriteControl(websocket.PingMessage, nil, time.Now().Add(writeWait)) != nil {
				return
			}

			lastPing = time.Now()
		}
	}
}

func streamCursorAfter(a, b string) bool {
	aTime, aSequence, _ := strings.Cut(a, "-")
	bTime, bSequence, _ := strings.Cut(b, "-")
	at, _ := strconv.ParseUint(aTime, 10, 64)
	bt, _ := strconv.ParseUint(bTime, 10, 64)
	as, _ := strconv.ParseUint(aSequence, 10, 64)
	bs, _ := strconv.ParseUint(bSequence, 10, 64)

	return at > bt || (at == bt && as > bs)
}
