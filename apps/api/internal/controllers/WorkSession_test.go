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

	"github.com/gorilla/websocket"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	db "github.com/rajandhamala/Maruvo/db/sqlc"
	"github.com/rajandhamala/Maruvo/internal/utils"
	"github.com/redis/go-redis/v9"
)

type workSessionFixture struct {
	db.DBTX
	post        db.Post
	escrowState string
	manual      bool
}

func (f workSessionFixture) QueryRow(_ context.Context, query string, _ ...any) pgx.Row {
	switch {
	case strings.Contains(query, "-- name: GetPostEscrow"):
		state := f.escrowState
		if state == "" {
			state = "confirmed"
		}
		return offerRow{value: db.PostEscrow{PostID: f.post.ID, State: state}}
	case strings.Contains(query, "-- name: GetPost"):
		return offerRow{value: f.post}
	case strings.Contains(query, "-- name: GetAgentControl"):
		if f.manual {
			return offerRow{value: db.PostAgentControl{Mode: "manual"}}
		}
		return offerRow{err: pgx.ErrNoRows}
	case strings.Contains(query, "-- name: IsPostReviewer"):
		return offerRow{value: false}
	default:
		return offerRow{err: pgx.ErrNoRows}
	}
}

func TestTwoAccountWorkSessionInvitationStatusAndStop(t *testing.T) {
	url := os.Getenv("PRESENCE_TEST_REDIS_URL")
	if url == "" {
		t.Skip("requires test Redis")
	}
	options, err := redis.ParseURL(url)
	if err != nil {
		t.Fatal(err)
	}
	cache := redis.NewClient(options)
	defer cache.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	post := time.Now().UnixNano()
	requester, worker := post+1, post+2
	requesterNonce, workerNonce := strings.Repeat("r", 32), strings.Repeat("w", 32)
	keys := []string{readyKey(post, requester), readyKey(post, worker), workInviteKey(requester), workInviteKey(worker), readyKey(post, requester) + ":status", readyKey(post, worker) + ":status", readyKey(post, requester) + ":cancelled:" + requesterNonce, readyKey(post, worker) + ":cancelled:" + workerNonce}
	defer cache.Del(context.Background(), keys...)
	fixture := workSessionFixture{post: db.Post{ID: post, UserID: requester, AcceptedBy: pgtype.Int8{Int64: worker, Valid: true}, Status: "in_progress", Title: "Isolated work-session fixture"}}
	c := &Controller{queries: db.New(fixture), redis: cache}
	t.Setenv("JWT_TOKEN", "isolated-work-session-fixture")
	token := func(user int64) string {
		t.Helper()
		value, _, err := utils.CreateUserToken(&utils.UserJWT{ID: fmt.Sprint(user)})
		if err != nil {
			t.Fatal(err)
		}
		return value
	}
	requesterToken, workerToken := token(requester), token(worker)
	mux := http.NewServeMux()
	mux.HandleFunc("GET /ws/work-invites", c.Auth(c.WorkInvites))
	mux.HandleFunc("POST /posts/{id}/agent-ready", c.Auth(c.AgentReadiness))
	mux.HandleFunc("GET /posts/{id}/agent-ready", c.Auth(c.AgentReadiness))
	server := httptest.NewServer(mux)
	defer server.Close()
	connect := func(token string) *websocket.Conn {
		t.Helper()
		conn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http")+"/ws/work-invites", http.Header{"Authorization": []string{"Bearer " + token}})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { conn.Close() })
		return conn
	}
	requesterSocket, workerSocket := connect(requesterToken), connect(workerToken)
	request := func(token string, body string, want int) map[string]any {
		t.Helper()
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, fmt.Sprintf("%s/posts/%d/agent-ready", server.URL, post), strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", "application/json")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var result map[string]any
		if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
			t.Fatal(err)
		}
		if resp.StatusCode != want {
			t.Fatalf("status=%d want=%d response=%v", resp.StatusCode, want, result)
		}
		return result
	}
	readUntil := func(conn *websocket.Conn, event, field string, value any) map[string]any {
		t.Helper()
		conn.SetReadDeadline(time.Now().Add(3 * time.Second))
		for i := 0; i < 8; i++ {
			var frame struct {
				Event string         `json:"event"`
				Data  map[string]any `json:"data"`
			}
			if err := conn.ReadJSON(&frame); err != nil {
				t.Fatal(err)
			}
			if frame.Event == event && frame.Data[field] == value {
				return frame.Data
			}
		}
		t.Fatal("expected session event was not delivered")
		return nil
	}
	request(token(requester+10), fmt.Sprintf(`{"ready":true,"nonce":%q}`, requesterNonce), 403)
	request(requesterToken, fmt.Sprintf(`{"ready":true,"invite":true,"nonce":%q,"status":"ready"}`, requesterNonce), 200)
	readUntil(workerSocket, "work.invite", "nonce", requesterNonce)
	accepted := request(workerToken, fmt.Sprintf(`{"ready":true,"nonce":%q,"target":%q,"status":"ready"}`, workerNonce, requesterNonce), 200)
	if accepted["pair"] != requesterNonce+":"+workerNonce || accepted["peer_ready"] != true {
		t.Fatal("accounts did not agree on the same session", accepted)
	}
	request(workerToken, fmt.Sprintf(`{"ready":true,"nonce":%q,"status":"working","detail":"Executing a task turn"}`, workerNonce), 200)
	readUntil(requesterSocket, "work.status", "status", "working")
	request(requesterToken, fmt.Sprintf(`{"ready":true,"nonce":%q,"status":"waiting_for_approval","detail":"Waiting for local action approval"}`, requesterNonce), 200)
	readUntil(workerSocket, "work.status", "status", "waiting_for_approval")
	request(requesterToken, fmt.Sprintf(`{"ready":false,"nonce":%q}`, requesterNonce), 200)
	readUntil(workerSocket, "work.invite", "cancelled", true)
	request(workerToken, fmt.Sprintf(`{"ready":true,"nonce":%q,"target":%q}`, workerNonce, requesterNonce), 409)
}

func TestReadyRejectsUnfundedClosedAndManualTasksBeforeRedis(t *testing.T) {
	post := db.Post{ID: 5, UserID: 3, AcceptedBy: pgtype.Int8{Int64: 1, Valid: true}, Status: "in_progress"}
	for _, test := range []struct {
		name, state    string
		manual, closed bool
	}{
		{name: "unfunded", state: "prepared"}, {name: "pending", state: "pending"}, {name: "manual", manual: true}, {name: "closed", closed: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := workSessionFixture{post: post, escrowState: test.state, manual: test.manual}
			if test.closed {
				fixture.post.Status = "completed"
			}
			c := &Controller{queries: db.New(fixture)}
			r := httptest.NewRequest(http.MethodPost, "/posts/5/agent-ready", strings.NewReader(fmt.Sprintf(`{"ready":true,"nonce":%q}`, strings.Repeat("a", 32))))
			r.SetPathValue("id", "5")
			r = r.WithContext(context.WithValue(r.Context(), utils.UserKey, &utils.UserJWT{ID: "1"}))
			response := httptest.NewRecorder()
			c.AgentReadiness(response, r)
			if response.Code != 409 {
				t.Fatalf("unsafe task became ready: status=%d body=%s", response.Code, response.Body.String())
			}
		})
	}
}
