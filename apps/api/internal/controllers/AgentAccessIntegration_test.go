package controller

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rajandhamala/Maruvo/internal/utils"
	"github.com/rajandhamala/Maruvo/pb"
	"github.com/redis/go-redis/v9"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"
)

type agentAccessFixture struct {
	ctx        context.Context
	controller *Controller
	pool       *pgxpool.Pool
	server     *httptest.Server
	postID     int64
	users      [3]int64
	tokens     [3]string
}

func newAgentAccessFixture(t *testing.T) *agentAccessFixture {
	t.Helper()

	databaseURL, redisURL := os.Getenv("AGENT_TEST_DATABASE_URL"), os.Getenv("AGENT_TEST_REDIS_URL")
	if databaseURL == "" || redisURL == "" {
		t.Skip("set AGENT_TEST_DATABASE_URL and AGENT_TEST_REDIS_URL to isolated, migrated services")
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	t.Cleanup(cancel)
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

	cache := redis.NewClient(options)

	t.Cleanup(func() { _ = cache.Close() })

	listener := bufconn.Listen(1 << 20)
	rpc := grpc.NewServer()

	pb.RegisterSolanaServiceServer(rpc, &deadlineRPC{})
	go rpc.Serve(listener)

	t.Cleanup(rpc.Stop)

	connection, err := grpc.NewClient("passthrough:///agent-access",
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return listener.Dial() }))
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = connection.Close() })

	c := NewController(pool, connection, nil, cache)

	f := &agentAccessFixture{ctx: ctx, controller: c, pool: pool}
	for i := range f.users {
		if err := pool.QueryRow(
			ctx,
			"INSERT INTO users(email,username) VALUES($1,$2) RETURNING id",
			uuid.NewString()+"@example.test",
			fmt.Sprintf("agent-owner-%d", i),
		).Scan(&f.users[i]); err != nil {
			t.Fatal(err)
		}

		token, _, err := utils.CreateUserToken(&utils.UserJWT{ID: fmt.Sprint(f.users[i])})
		if err != nil {
			t.Fatal(err)
		}

		f.tokens[i] = token
	}

	if err := pool.QueryRow(ctx, `INSERT INTO posts(user_id,title,description,cost_lamports,end_time,level)
		VALUES($1,'agent access test','Return a verified result',1,NOW()+INTERVAL '1 day','easy') RETURNING id`,
		f.users[0]).Scan(&f.postID); err != nil {
		t.Fatal(err)
	}

	if _, err := pool.Exec(
		ctx,
		"UPDATE posts SET accepted_by=$2, status='negotiating' WHERE id=$1",
		f.postID,
		f.users[1],
	); err != nil {
		t.Fatal(err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /me", c.GetMe)
	mux.HandleFunc("POST /posts/{id}/agents", c.Auth(c.CreateAgentGrant))
	mux.HandleFunc("GET /posts/{id}/agents", c.Auth(c.ListAgentGrants))
	mux.HandleFunc("POST /agents/{grant}/revoke", c.Auth(c.RevokeAgentGrant))
	mux.HandleFunc("GET /posts/{id}/workspace", c.Auth(c.GetWorkspace))
	mux.HandleFunc("GET /posts/{id}/history", c.Auth(c.WorkspaceHistory))
	mux.HandleFunc("POST /posts/{id}/messages", c.Auth(c.WorkspaceMessage))
	mux.HandleFunc("POST /posts/{id}/files", c.Auth(c.UploadWorkspaceFile))
	mux.HandleFunc("GET /posts/{id}/files/{file}", c.Auth(c.DownloadWorkspaceFile))
	mux.HandleFunc("POST /posts/{id}/submit", c.Auth(c.SubmitWorkspace))
	mux.HandleFunc("POST /posts/{id}/review/changes", c.Auth(c.RequestPostChanges))
	mux.HandleFunc("GET /ws", c.Auth(c.WsHandler))
	mux.HandleFunc("GET /ws/files", c.Auth(c.WorkspaceFileSocket))
	mux.HandleFunc("POST /posts/info", c.Auth(c.PostInfo))

	for _, pattern := range []string{"POST /posts/create", "POST /posts/feed", "POST /posts/accept", "POST /posts/recover", "POST /posts/fund", "POST /posts/fund/submit", "POST /posts/{id}/settle", "POST /posts/{id}/settle/submit", "GET /wallet", "POST /wallet/link", "POST /oauth/github/link"} {
		mux.HandleFunc(pattern, c.Auth(func(w http.ResponseWriter, r *http.Request) {
			t.Error("agent reached an owner-only handler")
			w.WriteHeader(200)
		}))
	}

	f.server = httptest.NewServer(mux)
	t.Cleanup(f.server.Close)

	return f
}

func (f *agentAccessFixture) request(
	t *testing.T,
	method, path, token string,
	payload any,
	status int,
) []byte {
	t.Helper()

	data, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}

	r, err := http.NewRequestWithContext(f.ctx, method, f.server.URL+path, bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}

	r.Header.Set("Authorization", "Bearer "+token)
	r.Header.Set("Content-Type", "application/json")

	response, err := http.DefaultClient.Do(r)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()

	body, err := io.ReadAll(response.Body)
	if err != nil || response.StatusCode != status {
		t.Fatalf(
			"%s %s: status %d, expected %d (%s, %v)",
			method,
			path,
			response.StatusCode,
			status,
			body,
			err,
		)
	}

	return body
}

func (f *agentAccessFixture) grant(t *testing.T, owner int, permissions ...string) (string, string) {
	t.Helper()
	body := f.request(
		t,
		"POST",
		fmt.Sprintf("/posts/%d/agents", f.postID),
		f.tokens[owner],
		map[string]any{
			"name":               fmt.Sprintf("harness-%d", owner),
			"permissions":        permissions,
			"expires_in_seconds": 3600,
		},
		201,
	)

	var result struct {
		Token string         `json:"token"`
		Grant agentGrantView `json:"grant"`
	}
	if err := json.Unmarshal(body, &result); err != nil || result.Token == "" || result.Grant.ID == "" {
		t.Fatal("grant response is incomplete")
	}

	return result.Token, result.Grant.ID
}

func (f *agentAccessFixture) socket(t *testing.T, token, path string) *websocket.Conn {
	t.Helper()

	conn, response, err := websocket.DefaultDialer.DialContext(f.ctx,
		strings.Replace(f.server.URL, "http://", "ws://", 1)+path,
		http.Header{"Authorization": []string{"Bearer " + token}})
	if err != nil {
		if response != nil {
			_ = response.Body.Close()
		}

		t.Fatal(err)
	}

	t.Cleanup(func() { _ = conn.Close() })

	return conn
}

func TestAgentAccessLifecycle(t *testing.T) {
	f := newAgentAccessFixture(t)
	path := fmt.Sprintf("/posts/%d", f.postID)
	readToken, readID := f.grant(t, 1, "read")
	workerToken, workerID := f.grant(t, 1, "read", "message", "upload", "submit")
	posterToken, _ := f.grant(t, 0, "read", "message", "upload")

	me := f.request(t, "GET", "/me", workerToken, nil, 200)
	if bytes.Contains(me, []byte("@example.test")) || !bytes.Contains(me, []byte(workerID)) {
		t.Fatal("agent identity leaked email or omitted its grant")
	}

	var hash string
	if err := f.pool.QueryRow(f.ctx, "SELECT token_hash FROM agent_grants WHERE id=$1", workerID).
		Scan(&hash); err != nil || hash != tokenHash(workerToken) ||
		hash == workerToken {
		t.Fatal("database did not store only the credential hash")
	}

	f.request(t, "GET", path+"/workspace", readToken, nil, 200)
	f.request(t, "GET", fmt.Sprintf("/posts/%d/workspace", f.postID+100000), workerToken, nil, 403)
	f.request(t, "POST", "/posts/info", workerToken, map[string]any{"id": f.postID + 100000}, 403)
	f.request(t, "POST", path+"/messages", readToken, map[string]string{"text": "denied"}, 403)
	f.request(t, "POST", path+"/submit", posterToken, map[string]any{"note": "denied"}, 403)

	for _, target := range []struct{ method, path string }{
		{"POST", "/posts/create"}, {"POST", "/posts/feed"}, {"POST", "/posts/accept"},
		{"POST", "/posts/recover"}, {"POST", "/posts/fund"}, {"POST", "/posts/fund/submit"},
		{"POST", path + "/settle"}, {"POST", path + "/settle/submit"}, {"GET", "/wallet"},
		{"POST", "/wallet/link"}, {"POST", "/oauth/github/link"},
		{"POST", path + "/agents"}, {"GET", path + "/agents"}, {"POST", "/agents/" + workerID + "/revoke"},
	} {
		f.request(t, target.method, target.path, workerToken, nil, 403)
	}

	for _, invalid := range []struct {
		owner, status int
		permissions   []string
	}{
		{0, 403, []string{"read", "submit"}}, {1, 403, []string{"read", "request-changes"}},
		{2, 403, []string{"read"}}, {1, 400, []string{"read", "fund"}}, {1, 400, []string{"message"}},
	} {
		f.request(
			t,
			"POST",
			path+"/agents",
			f.tokens[invalid.owner],
			map[string]any{
				"name":               "invalid",
				"permissions":        invalid.permissions,
				"expires_in_seconds": 3600,
			},
			invalid.status,
		)
	}

	f.request(t, "POST", path+"/messages", workerToken,
		map[string]string{"text": "scoped message", "message_id": "scoped-id"}, 201)

	workspace := f.request(t, "GET", path+"/workspace", posterToken, nil, 200)
	if !bytes.Contains(workspace, []byte(workerID)) || !bytes.Contains(workspace, []byte("scoped message")) {
		t.Fatal("chat did not preserve agent attribution")
	}

	f.request(
		t,
		"POST",
		path+"/submit",
		workerToken,
		map[string]any{"note": "ready", "submission_version": 0},
		409,
	)

	if _, err := f.pool.Exec(
		f.ctx,
		`INSERT INTO post_escrows(post_id,address,program_id,reviewer,network,transaction,last_valid_block_height,fee_lamports,storage_lamports,state)
		VALUES($1,$2,'fixture','fixture','localnet','fixture',1,0,0,'confirmed')`,
		f.postID,
		uuid.NewString(),
	); err != nil {
		t.Fatal(err)
	}

	if _, err := f.pool.Exec(
		f.ctx,
		"UPDATE posts SET status='in_progress' WHERE id=$1",
		f.postID,
	); err != nil {
		t.Fatal(err)
	}

	content := []byte("verified artifact")
	sum := sha256.Sum256(content)
	fileSocketPath := fmt.Sprintf("/ws/files?post_id=%d", f.postID)

	readonly := f.socket(t, readToken, fileSocketPath)
	if err := readonly.WriteJSON(
		fileRequest{
			Action:  "upload",
			Name:    "denied.txt",
			Size:    int64(len(content)),
			SHA256:  hex.EncodeToString(sum[:]),
			Purpose: "output",
		},
	); err != nil {
		t.Fatal(err)
	}

	var frame struct {
		Event string          `json:"event"`
		Data  json.RawMessage `json:"data"`
	}
	if err := readonly.ReadJSON(
		&frame,
	); err != nil || frame.Event != "error" ||
		!bytes.Contains(frame.Data, []byte("403")) {
		t.Fatal("read-only file socket accepted an upload")
	}

	upload := f.socket(t, workerToken, fileSocketPath)
	if err := upload.WriteJSON(
		fileRequest{
			Action:  "upload",
			Name:    "result.txt",
			Size:    int64(len(content)),
			SHA256:  hex.EncodeToString(sum[:]),
			Purpose: "output",
		},
	); err != nil {
		t.Fatal(err)
	}

	if err := upload.WriteMessage(websocket.BinaryMessage, content); err != nil {
		t.Fatal(err)
	}

	if err := upload.ReadJSON(
		&frame,
	); err != nil || frame.Event != "file.complete" ||
		!bytes.Contains(frame.Data, []byte(workerID)) {
		t.Fatal("uploaded file lost its agent attribution")
	}

	var file struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(frame.Data, &file); err != nil {
		t.Fatal(err)
	}

	f.request(t, "GET", path+"/files/"+file.ID, readToken, nil, 200)
	f.request(
		t,
		"POST",
		path+"/submit",
		workerToken,
		map[string]any{
			"note":               "ready",
			"submission_version": 0,
			"files":              []string{file.ID},
			"input_files":        []string{},
		},
		201,
	)

	var attributed bool
	if err := f.pool.QueryRow(f.ctx, `SELECT EXISTS(SELECT 1 FROM workspace_events WHERE post_id=$1 AND kind='work.submitted' AND data->>'agent_grant_id'=$2)`, f.postID, workerID).
		Scan(&attributed); err != nil ||
		!attributed {
		t.Fatal("submission lost its agent attribution")
	}

	f.request(t, "POST", "/agents/"+workerID+"/revoke", f.tokens[2], nil, 404)
	f.request(t, "GET", path+"/agents", f.tokens[2], nil, 200)

	events := f.socket(t, workerToken, fmt.Sprintf("/ws?post_id=%d", f.postID))
	if err := events.ReadJSON(&frame); err != nil || frame.Event != "connected" {
		t.Fatal("agent could not connect to task events")
	}

	partial := f.socket(t, workerToken, fileSocketPath)
	if err := partial.WriteJSON(
		fileRequest{
			Action:  "upload",
			Name:    "partial.txt",
			Size:    100,
			SHA256:  hex.EncodeToString(sum[:]),
			Purpose: "shared",
		},
	); err != nil {
		t.Fatal(err)
	}

	f.request(t, "POST", "/agents/"+workerID+"/revoke", f.tokens[1], nil, 200)
	f.request(t, "POST", "/agents/"+workerID+"/revoke", f.tokens[1], nil, 200)
	f.request(t, "GET", "/me", workerToken, nil, 401)
	f.request(t, "GET", path+"/workspace", workerToken, nil, 401)

	for _, conn := range []*websocket.Conn{events, partial} {
		_ = conn.SetReadDeadline(time.Now().Add(7 * time.Second))
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				if timeout, ok := err.(interface{ Timeout() bool }); ok && timeout.Timeout() {
					t.Fatal("revoked agent connection stayed open")
				}

				break
			}
		}
	}

	var partialFiles int
	if err := f.pool.QueryRow(f.ctx, "SELECT count(*) FROM workspace_files WHERE post_id=$1 AND name='partial.txt'", f.postID).
		Scan(&partialFiles); err != nil ||
		partialFiles != 0 {
		t.Fatal("revoked partial upload was persisted")
	}

	if _, err := f.pool.Exec(
		f.ctx,
		"UPDATE agent_grants SET created_at=NOW()-INTERVAL '2 hours', expires_at=NOW()-INTERVAL '1 hour' WHERE id=$1",
		readID,
	); err != nil {
		t.Fatal(err)
	}

	f.request(t, "GET", "/me", readToken, nil, 401)
	f.request(t, "GET", path+"/workspace", readToken, nil, 401)
	f.request(t, "GET", path+"/workspace", posterToken, nil, 200)
	f.request(t, "GET", path+"/workspace", f.tokens[1], nil, 200)
}

func TestAgentExpiryAndReviewerPermissions(t *testing.T) {
	f := newAgentAccessFixture(t)

	path := fmt.Sprintf("/posts/%d", f.postID)
	for _, lifetime := range []int64{0, 59, 604801} {
		f.request(t, "POST", path+"/agents", f.tokens[1], map[string]any{
			"name": "invalid-expiry", "permissions": []string{"read"}, "expires_in_seconds": lifetime,
		}, 400)
	}

	token, id := f.grant(t, 1, "read")
	events := f.socket(t, token, fmt.Sprintf("/ws?post_id=%d", f.postID))

	var frame WsResponse
	if err := events.ReadJSON(&frame); err != nil || frame.Event != "connected" {
		t.Fatal("expiry fixture could not connect to events")
	}

	file := f.socket(t, token, fmt.Sprintf("/ws/files?post_id=%d", f.postID))
	if _, err := f.pool.Exec(
		f.ctx,
		"UPDATE agent_grants SET created_at=NOW()-INTERVAL '2 hours', expires_at=NOW()-INTERVAL '1 hour' WHERE id=$1",
		id,
	); err != nil {
		t.Fatal(err)
	}

	f.request(t, "GET", "/me", token, nil, 401)

	for _, conn := range []*websocket.Conn{events, file} {
		_ = conn.SetReadDeadline(time.Now().Add(7 * time.Second))
		if _, _, err := conn.ReadMessage(); err == nil {
			t.Fatal("expired agent connection delivered a frame")
		} else if timeout, ok := err.(interface{ Timeout() bool }); ok && timeout.Timeout() {
			t.Fatal("expired agent connection stayed open")
		}
	}

	address := uuid.NewString()
	if _, err := f.pool.Exec(
		f.ctx,
		"INSERT INTO wallets(user_id,address) VALUES($1,$2)",
		f.users[0],
		address,
	); err != nil {
		t.Fatal(err)
	}

	if _, err := f.pool.Exec(
		f.ctx,
		`INSERT INTO post_escrows(post_id,address,program_id,reviewer,network,transaction,last_valid_block_height,fee_lamports,storage_lamports,state)
		VALUES($1,$2,'fixture',$3,'localnet','fixture',1,0,0,'confirmed')`,
		f.postID,
		uuid.NewString(),
		address,
	); err != nil {
		t.Fatal(err)
	}

	if _, err := f.pool.Exec(
		f.ctx,
		"UPDATE posts SET status='in_progress' WHERE id=$1",
		f.postID,
	); err != nil {
		t.Fatal(err)
	}

	if _, err := f.pool.Exec(
		f.ctx,
		"UPDATE post_workspaces SET submission_version=1, review_state='submitted', submission='ready' WHERE post_id=$1",
		f.postID,
	); err != nil {
		t.Fatal(err)
	}

	reviewer, reviewerID := f.grant(t, 0, "read", "request-changes")
	f.request(t, "POST", path+"/review/changes", reviewer,
		map[string]any{"note": "add regression", "submission_version": 2}, 409)
	f.request(t, "POST", path+"/review/changes", reviewer,
		map[string]any{"note": "add regression", "submission_version": 1}, 201)

	var attributed bool
	if err := f.pool.QueryRow(f.ctx, `SELECT EXISTS(SELECT 1 FROM workspace_events WHERE post_id=$1 AND kind='review.changes_requested' AND data->>'agent_grant_id'=$2)`, f.postID, reviewerID).
		Scan(&attributed); err != nil ||
		!attributed {
		t.Fatal("revision request lost its acting-agent identity")
	}
}
