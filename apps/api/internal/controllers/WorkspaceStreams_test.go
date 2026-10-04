package controller

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	db "github.com/rajandhamala/Maruvo/db/sqlc"
	middleware "github.com/rajandhamala/Maruvo/internal/middlewares"
	"github.com/rajandhamala/Maruvo/internal/utils"
	"github.com/redis/go-redis/v9"
)

func TestWorkspaceStreams(t *testing.T) {
	databaseURL, redisURL := os.Getenv("STREAM_TEST_DATABASE_URL"), os.Getenv("STREAM_TEST_REDIS_URL")
	if databaseURL == "" || redisURL == "" {
		t.Skip("set STREAM_TEST_DATABASE_URL and STREAM_TEST_REDIS_URL to isolated, migrated test services")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	t.Setenv("JWT_TOKEN", uuid.NewString())

	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(pool.Close)

	options, err := redis.ParseURL(redisURL)
	if err != nil {
		t.Fatal(err)
	}

	client := redis.NewClient(options)

	t.Cleanup(func() { client.Close() })

	c := NewController(pool, nil, nil, client)

	users := make([]int64, 3)
	tokens := make([]string, 3)

	for i := range users {
		err := pool.QueryRow(ctx, "INSERT INTO users(email, username) VALUES($1, 'stream-test') RETURNING id",
			uuid.NewString()+"@example.test").Scan(&users[i])
		if err != nil {
			t.Fatal(err)
		}

		token, _, err := utils.CreateUserToken(&utils.UserJWT{ID: fmt.Sprint(users[i])})
		if err != nil {
			t.Fatal(err)
		}

		tokens[i] = token
	}

	var postID int64

	err = pool.QueryRow(ctx, `INSERT INTO posts(user_id, title, cost_lamports, end_time, level)
        VALUES($1, 'stream test', 1, NOW() + INTERVAL '1 day', 'easy') RETURNING id`, users[0]).Scan(&postID)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := pool.Exec(ctx, "UPDATE posts SET accepted_by = $2, status = 'negotiating' WHERE id = $1",
		postID, users[1]); err != nil {
		t.Fatal(err)
	}

	_, err = c.queries.AppendWorkspaceEvent(ctx, db.AppendWorkspaceEventParams{
		PostID: postID, ActorID: pgtype.Int8{Int64: users[0], Valid: true},
		Kind: "message", Data: json.RawMessage(`{"text":"legacy message"}`),
	})
	if err != nil {
		t.Fatal(err)
	}

	if err := c.publishDatabaseEvents(ctx); err != nil {
		t.Fatal(err)
	}

	_, initialCursor, err := c.recentStreamEvents(ctx, postID)
	if err != nil {
		t.Fatal(err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /ws", middleware.Auth(c.WsHandler))
	mux.HandleFunc("GET /posts/{id}/workspace", middleware.Auth(c.GetWorkspace))
	mux.HandleFunc("POST /posts/{id}/messages", middleware.Auth(c.WorkspaceMessage))
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)

	read := func(conn *websocket.Conn) db.WorkspaceEvent {
		t.Helper()
		conn.SetReadDeadline(time.Now().Add(3 * time.Second))

		var frame struct {
			Event string          `json:"event"`
			Data  json.RawMessage `json:"data"`
		}
		if err := conn.ReadJSON(&frame); err != nil {
			t.Fatal(err)
		}

		if frame.Event != "workspace.event" {
			t.Fatalf("unexpected frame: %s", frame.Event)
		}

		var event db.WorkspaceEvent
		if err := json.Unmarshal(frame.Data, &event); err != nil {
			t.Fatal(err)
		}

		return event
	}
	dial := func(token, cursor string) *websocket.Conn {
		t.Helper()

		address := strings.Replace(server.URL, "http://", "ws://", 1) +
			fmt.Sprintf("/ws?post_id=%d&cursor=%s", postID, cursor)

		conn, _, err := websocket.DefaultDialer.DialContext(ctx, address,
			http.Header{"Authorization": []string{"Bearer " + token}})
		if err != nil {
			t.Fatal(err)
		}

		t.Cleanup(func() { conn.Close() })
		conn.SetReadDeadline(time.Now().Add(3 * time.Second))

		var frame WsResponse
		if err := conn.ReadJSON(&frame); err != nil || frame.Event != "connected" {
			t.Fatalf("connection handshake failed: %v", err)
		}

		return conn
	}
	poster := dial(tokens[0], initialCursor)
	worker := dial(tokens[1], initialCursor)

	acquisitions := pool.Stat().AcquireCount()

	time.Sleep(1100 * time.Millisecond)

	if pool.Stat().AcquireCount() != acquisitions {
		t.Fatal("idle WebSockets queried PostgreSQL")
	}

	send := func(text string) {
		t.Helper()

		request, err := http.NewRequestWithContext(ctx, http.MethodPost,
			fmt.Sprintf("%s/posts/%d/messages", server.URL, postID),
			strings.NewReader(`{"text":"`+text+`"}`))
		if err != nil {
			t.Fatal(err)
		}

		request.Header.Set("Authorization", "Bearer "+tokens[0])

		response, err := http.DefaultClient.Do(request)
		if err != nil {
			t.Fatal(err)
		}

		response.Body.Close()

		if response.StatusCode != http.StatusCreated {
			t.Fatalf("message failed: %d", response.StatusCode)
		}
	}
	send("first")

	first, copy := read(poster), read(worker)
	if first.Kind != "message" || first.StreamID != copy.StreamID || first.ID != 0 {
		t.Fatal("both users did not receive the same Redis-first message")
	}

	worker.Close()
	send("second")

	second := read(poster)

	worker = dial(tokens[1], first.StreamID.String)
	if replay := read(worker); replay.StreamID != second.StreamID {
		t.Fatal("reconnect did not replay the missed message")
	}

	countMessages := func() int {
		t.Helper()

		var count int
		if err := pool.QueryRow(ctx, "SELECT COUNT(*) FROM workspace_events WHERE post_id = $1 AND kind = 'message'",
			postID).
			Scan(&count); err != nil {
			t.Fatal(err)
		}

		return count
	}
	if countMessages() != 1 {
		t.Fatal("chat was written to PostgreSQL before the archive worker ran")
	}

	request, _ := http.NewRequestWithContext(ctx, http.MethodGet,
		fmt.Sprintf("%s/posts/%d/workspace", server.URL, postID), nil)
	request.Header.Set("Authorization", "Bearer "+tokens[0])

	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}

	var snapshot struct {
		Cursor string              `json:"cursor"`
		Events []db.WorkspaceEvent `json:"events"`
	}

	err = json.NewDecoder(response.Body).Decode(&snapshot)
	response.Body.Close()

	if err != nil || response.StatusCode != 200 || snapshot.Cursor != second.StreamID.String ||
		len(snapshot.Events) != 4 {
		t.Fatalf("workspace snapshot omitted or duplicated unarchived messages: %v", err)
	}

	if err := client.XGroupCreateMkStream(ctx, workspaceArchiveStream, workspaceArchiveGroup, "0").
		Err(); err != nil && !redis.HasErrorPrefix(err, "BUSYGROUP") {
		t.Fatal(err)
	}

	streams, err := client.XReadGroup(ctx, &redis.XReadGroupArgs{
		Group: workspaceArchiveGroup, Consumer: "failed-worker",
		Streams: []string{workspaceArchiveStream, ">"}, Count: 100, Block: -1,
	}).Result()
	if err != nil {
		t.Fatal(err)
	}

	messages := streams[0].Messages
	failed := *c
	failed.redis = redis.NewClient(options)
	failed.redis.Close()

	if err := failed.archiveMessageBatch(ctx, messages); err == nil {
		t.Fatal("expected acknowledgement failure after the database commit")
	}

	if countMessages() != 3 {
		t.Fatal("message batch was not committed")
	}

	claimed, _, err := client.XAutoClaim(ctx, &redis.XAutoClaimArgs{
		Stream: workspaceArchiveStream, Group: workspaceArchiveGroup,
		Consumer: "recovered-worker", MinIdle: 0, Start: "0-0", Count: 100,
	}).Result()
	if err != nil || len(claimed) != 2 {
		t.Fatalf("pending messages were not recovered: %v", err)
	}

	if err := c.archiveMessageBatch(ctx, claimed); err != nil {
		t.Fatal(err)
	}

	if countMessages() != 3 {
		t.Fatal("retry duplicated message history")
	}

	pending, err := client.XPending(ctx, workspaceArchiveStream, workspaceArchiveGroup).Result()
	if err != nil || pending.Count != 0 {
		t.Fatalf("persisted messages were not acknowledged: %v", err)
	}

	if _, err := pool.Exec(ctx, "UPDATE posts SET status = 'in_progress' WHERE id = $1", postID); err != nil {
		t.Fatal(err)
	}

	unpublished, err := c.queries.UnpublishedWorkspaceEvents(ctx)
	if err != nil || len(unpublished) != 1 {
		t.Fatalf("transactional event was not queued: %v", err)
	}

	if _, err := c.publishEvent(
		ctx,
		unpublished[0],
		fmt.Sprintf("db:%d", unpublished[0].ID),
		true,
	); err != nil {
		t.Fatal(err)
	}

	length, err := client.XLen(ctx, workspaceStreamKey(postID)).Result()
	if err != nil {
		t.Fatal(err)
	}

	if err := c.publishDatabaseEvents(ctx); err != nil {
		t.Fatal(err)
	}

	if actual := client.XLen(ctx, workspaceStreamKey(postID)).Val(); actual != length {
		t.Fatal("retry after Redis publication duplicated a transactional event")
	}

	for _, conn := range []*websocket.Conn{poster, worker} {
		if event := read(conn); event.Kind != "task.status" {
			t.Fatal("transactional event did not reach both users")
		}
	}

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}

	_, err = db.New(tx).AppendWorkspaceEvent(ctx, db.AppendWorkspaceEventParams{
		PostID: postID, ActorID: pgtype.Int8{Int64: users[0], Valid: true},
		Kind: "review.changes_requested", Data: json.RawMessage(`{"note":"rolled back"}`),
	})
	if err != nil {
		tx.Rollback(ctx)
		t.Fatal(err)
	}

	if err := tx.Rollback(ctx); err != nil {
		t.Fatal(err)
	}

	if err := c.publishDatabaseEvents(ctx); err != nil {
		t.Fatal(err)
	}

	if actual := client.XLen(ctx, workspaceStreamKey(postID)).Val(); actual != length {
		t.Fatal("rolled-back database event was published")
	}

	workerCtx, stop := context.WithCancel(ctx)

	done := make(chan struct{})
	go func() {
		defer close(done)

		c.RunWorkspaceWorkers(workerCtx)
	}()

	t.Cleanup(func() { stop(); <-done })

	if _, err := pool.Exec(ctx, "UPDATE posts SET status = 'completed' WHERE id = $1", postID); err != nil {
		t.Fatal(err)
	}

	for _, conn := range []*websocket.Conn{poster, worker} {
		if event := read(conn); event.Kind != "task.status" {
			t.Fatal("transactional task change did not reach both clients")
		}
	}

	address := strings.Replace(server.URL, "http://", "ws://", 1) + fmt.Sprintf("/ws?post_id=%d", postID)

	_, denied, err := websocket.DefaultDialer.DialContext(ctx, address,
		http.Header{"Authorization": []string{"Bearer " + tokens[2]}})
	if denied != nil {
		denied.Body.Close()
	}

	if err == nil || denied == nil || denied.StatusCode != http.StatusForbidden {
		t.Fatal("unrelated user was allowed into the private stream")
	}
}
