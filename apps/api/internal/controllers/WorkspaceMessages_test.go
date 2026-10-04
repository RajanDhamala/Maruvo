package controller

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	middleware "github.com/rajandhamala/Maruvo/internal/middlewares"
	"github.com/rajandhamala/Maruvo/internal/utils"
	"github.com/redis/go-redis/v9"
)

func TestWorkspaceMessageRetries(t *testing.T) {
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
		if err := pool.QueryRow(ctx, "INSERT INTO users(email, username) VALUES($1, 'retry-test') RETURNING id",
			uuid.NewString()+"@example.test").
			Scan(&users[i]); err != nil {
			t.Fatal(err)
		}

		tokens[i], _, err = utils.CreateUserToken(&utils.UserJWT{ID: fmt.Sprint(users[i])})
		if err != nil {
			t.Fatal(err)
		}
	}

	posts := make([]int64, 2)
	for i := range posts {
		if err := pool.QueryRow(ctx, `INSERT INTO posts(user_id, title, cost_lamports, end_time, level)
            VALUES($1, 'retry test', 1, NOW() + INTERVAL '1 day', 'easy') RETURNING id`,
			users[0]).Scan(&posts[i]); err != nil {
			t.Fatal(err)
		}

		if _, err := pool.Exec(ctx, "UPDATE posts SET accepted_by = $2, status = 'negotiating' WHERE id = $1",
			posts[i], users[1]); err != nil {
			t.Fatal(err)
		}
	}

	mux := http.NewServeMux()
	mux.HandleFunc("POST /posts/{id}/messages", middleware.Auth(c.WorkspaceMessage))
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)

	type receipt struct {
		MessageID string `json:"message_id"`
		StreamID  string `json:"stream_id"`
	}

	send := func(postID int64, token, text, messageID string, wantStatus int) receipt {
		t.Helper()

		body, _ := json.Marshal(map[string]string{"text": text, "message_id": messageID})

		request, err := http.NewRequestWithContext(ctx, http.MethodPost,
			fmt.Sprintf("%s/posts/%d/messages", server.URL, postID), strings.NewReader(string(body)))
		if err != nil {
			t.Fatal(err)
		}

		request.Header.Set("Authorization", "Bearer "+token)

		response, err := http.DefaultClient.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()

		if response.StatusCode != wantStatus {
			t.Errorf("message returned %d, want %d", response.StatusCode, wantStatus)
		}

		var result receipt
		if err := json.NewDecoder(response.Body).Decode(&result); err != nil {
			t.Fatal(err)
		}

		return result
	}

	archiveBefore := client.XLen(ctx, workspaceArchiveStream).Val()
	messageID := uuid.NewString()

	first := send(posts[0], tokens[0], " hello ", messageID, http.StatusCreated)
	if first.MessageID != messageID || !validStreamCursor(first.StreamID) {
		t.Fatalf("invalid receipt: %+v", first)
	}

	var retries sync.WaitGroup
	for range 12 {
		retries.Go(func() {
			if got := send(posts[0], tokens[0], "hello", messageID, http.StatusCreated); got != first {
				t.Errorf("retry returned %+v, want %+v", got, first)
			}
		})
	}

	retries.Wait()

	if client.XLen(ctx, workspaceStreamKey(posts[0])).Val() != 1 ||
		client.XLen(ctx, workspaceArchiveStream).Val() != archiveBefore+1 {
		t.Fatal("concurrent HTTP retries duplicated live or archived chat")
	}

	send(posts[0], tokens[0], "changed", messageID, http.StatusConflict)
	send(posts[0], tokens[2], "hello", messageID, http.StatusForbidden)

	for _, invalid := range []string{"invalid id", strings.Repeat("a", 129)} {
		send(posts[0], tokens[0], "hello", invalid, http.StatusBadRequest)
	}

	worker := send(posts[0], tokens[1], "hello", messageID, http.StatusCreated)
	if worker.StreamID == first.StreamID {
		t.Fatal("one sender's message ID deduplicated another sender's message")
	}

	send(posts[1], tokens[0], "hello", messageID, http.StatusCreated)

	if client.XLen(ctx, workspaceStreamKey(posts[1])).Val() != 1 {
		t.Fatal("message ID was not scoped to the task")
	}

	if _, err := pool.Exec(ctx, "UPDATE posts SET status = 'completed' WHERE id = $1", posts[0]); err != nil {
		t.Fatal(err)
	}

	if got := send(posts[0], tokens[0], "hello", messageID, http.StatusCreated); got != first {
		t.Fatal("a retry after task completion did not return the original receipt")
	}

	send(posts[0], tokens[0], "new", uuid.NewString(), http.StatusConflict)
	send(posts[0], tokens[0], "changed", messageID, http.StatusConflict)

	legacy := send(posts[1], tokens[0], "legacy", "", http.StatusCreated)
	if got := send(posts[1], tokens[0], "legacy", "", http.StatusCreated); got.MessageID == legacy.MessageID {
		t.Fatal("distinct legacy requests were deduplicated without a client ID")
	}

	if client.XLen(ctx, workspaceArchiveStream).Val() != archiveBefore+5 {
		t.Fatal("conflicts or retries added archive entries")
	}

	err = client.XGroupCreateMkStream(ctx, workspaceArchiveStream, workspaceArchiveGroup, "0").Err()
	if err != nil && !redis.HasErrorPrefix(err, "BUSYGROUP") {
		t.Fatal(err)
	}

	streams, err := client.XReadGroup(ctx, &redis.XReadGroupArgs{
		Group: workspaceArchiveGroup, Consumer: "retry-test",
		Streams: []string{workspaceArchiveStream, ">"}, Count: 100, Block: -1,
	}).Result()
	if err != nil {
		t.Fatal(err)
	}

	if err := c.archiveMessageBatch(ctx, streams[0].Messages); err != nil {
		t.Fatal(err)
	}

	for i, want := range []int{2, 3} {
		var count int
		if err := pool.QueryRow(ctx, "SELECT COUNT(*) FROM workspace_events WHERE post_id = $1 AND kind = 'message'",
			posts[i]).
			Scan(&count); err != nil ||
			count != want {
			t.Fatalf("archived %d messages for task %d, want %d: %v", count, posts[i], want, err)
		}
	}

	if got := send(posts[0], tokens[0], "hello", messageID, http.StatusCreated); got != first {
		t.Fatal("archival changed the retry receipt")
	}

	if err := c.publishDatabaseEvents(ctx); err != nil {
		t.Fatal(err)
	}
}
