package controller

import (
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
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	db "github.com/rajandhamala/Maruvo/db/sqlc"
	middleware "github.com/rajandhamala/Maruvo/internal/middlewares"
	"github.com/rajandhamala/Maruvo/internal/utils"
	"github.com/rajandhamala/Maruvo/pb"
	"github.com/redis/go-redis/v9"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
)

type recoveryRPC struct {
	pb.UnimplementedSolanaServiceServer
	state atomic.Value
}

func (r *recoveryRPC) PrepareEscrow(
	_ context.Context,
	request *pb.PrepareEscrowRequest,
) (*pb.PreparedEscrow, error) {
	return &pb.PreparedEscrow{
		Address:              fmt.Sprintf("recovery-fixture-%d", request.PostId),
		ProgramId:            "program",
		Reviewer:             "reviewer",
		Network:              "localnet",
		Transaction:          "plan",
		LastValidBlockHeight: 100,
	}, nil
}

func (r *recoveryRPC) CheckEscrowRecovery(
	_ context.Context,
	_ *pb.CheckEscrowRequest,
) (*pb.EscrowState, error) {
	state := r.state.Load().(string)
	if state == "unavailable" {
		return nil, status.Error(codes.Unavailable, "fixture RPC unavailable")
	}

	if state == "unimplemented" {
		return nil, status.Error(codes.Unimplemented, "old Rust service")
	}

	return &pb.EscrowState{State: state}, nil
}

func TestRecoveryPayloadValidation(t *testing.T) {
	c := &Controller{}

	for _, body := range []string{`{}`, `{"id":1,"action":"refund"}`, `{"id":-1,"action":"cancel"}`,
		`{"id":1,"action":"reopen","end_time":"invalid"}`, strings.Repeat("x", 4097)} {
		r := httptest.NewRequest(http.MethodPost, "/posts/recover", strings.NewReader(body))
		r = r.WithContext(context.WithValue(r.Context(), utils.UserKey, &utils.UserJWT{ID: "1"}))
		w := httptest.NewRecorder()
		c.RecoverPost(w, r)

		if w.Code != 400 {
			t.Fatalf("invalid recovery payload was accepted: %d", w.Code)
		}
	}

	w := httptest.NewRecorder()
	c.RecoverPost(w, httptest.NewRequest(http.MethodPost, "/posts/recover", strings.NewReader(`{}`)))

	if w.Code != 401 {
		t.Fatal("recovery must require authentication")
	}
}

func TestUnfundedTaskRecovery(t *testing.T) {
	databaseURL, redisURL := os.Getenv("RECOVERY_TEST_DATABASE_URL"), os.Getenv("RECOVERY_TEST_REDIS_URL")
	if databaseURL == "" || redisURL == "" {
		t.Skip("set RECOVERY_TEST_DATABASE_URL and RECOVERY_TEST_REDIS_URL to isolated, migrated services")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
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

	redisClient := redis.NewClient(options)

	t.Cleanup(func() { redisClient.Close() })

	rpc := &recoveryRPC{}
	rpc.state.Store("prepared")

	listener := bufconn.Listen(1 << 20)
	grpcServer := grpc.NewServer()

	pb.RegisterSolanaServiceServer(grpcServer, rpc)
	go grpcServer.Serve(listener)

	t.Cleanup(grpcServer.Stop)

	connection, err := grpc.NewClient(
		"passthrough:///recovery",
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return listener.Dial() }),
	)
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { connection.Close() })

	c := NewController(pool, connection, nil, redisClient)

	users, tokens := make([]int64, 3), make([]string, 3)
	for i := range users {
		err := pool.QueryRow(ctx, "INSERT INTO users(email, username) VALUES($1, 'recovery-test') RETURNING id",
			uuid.NewString()+"@example.test").
			Scan(&users[i])
		if err != nil {
			t.Fatal(err)
		}

		if _, err := c.queries.LinkWallet(
			ctx,
			db.LinkWalletParams{UserID: users[i], Address: uuid.NewString()},
		); err != nil {
			t.Fatal(err)
		}

		token, _, err := utils.CreateUserToken(&utils.UserJWT{ID: fmt.Sprint(users[i])})
		if err != nil {
			t.Fatal(err)
		}

		tokens[i] = token
	}

	mux := http.NewServeMux()
	mux.HandleFunc("POST /posts/recover", middleware.Auth(c.RecoverPost))
	mux.HandleFunc("POST /posts/accept", middleware.Auth(c.AcceptPost))
	mux.HandleFunc("POST /posts/fund", middleware.Auth(c.PreparePostFunding))
	mux.HandleFunc("GET /posts/{id}/workspace", middleware.Auth(c.GetWorkspace))
	mux.HandleFunc("POST /posts/{id}/messages", middleware.Auth(c.WorkspaceMessage))
	mux.HandleFunc("GET /posts/{id}/files/{file}", middleware.Auth(c.DownloadWorkspaceFile))
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)

	request := func(user int, method, path, body string) (int, []byte) {
		t.Helper()

		r, err := http.NewRequestWithContext(ctx, method, server.URL+path, strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}

		r.Header.Set("Authorization", "Bearer "+tokens[user])

		response, err := http.DefaultClient.Do(r)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()

		data, err := io.ReadAll(response.Body)
		if err != nil {
			t.Fatal(err)
		}

		return response.StatusCode, data
	}
	createAccepted := func() db.Post {
		t.Helper()

		post, err := c.queries.CreatePost(
			ctx,
			db.CreatePostParams{
				UserID:             users[0],
				Title:              "Recovery fixture",
				CostLamports:       1000000,
				EndTime:            pgtype.Timestamptz{Time: time.Now().Add(time.Hour), Valid: true},
				Status:             db.PostStatusOpen,
				Level:              db.PostLevelEasy,
				Description:        "Exact\n\tUTF-8: café",
				AcceptanceCriteria: "Tests pass",
				InputFiles:         []string{"input.txt"},
				ExpectedOutputs:    []string{"patch.diff"},
			},
		)
		if err != nil {
			t.Fatal(err)
		}

		post, err = c.queries.AcceptPost(ctx, db.AcceptPostParams{PostID: post.ID,
			WorkerID: pgtype.Int8{Int64: users[1], Valid: true}})
		if err != nil {
			t.Fatal(err)
		}

		return post
	}
	decode := func(data []byte) db.Post {
		t.Helper()

		var result struct {
			Post db.Post `json:"post"`
		}
		if err := json.Unmarshal(data, &result); err != nil {
			t.Fatal(err)
		}

		return result.Post
	}

	post := createAccepted()
	fileID, content := uuid.NewString(), "Private old-worker content"
	hash := sha256.Sum256([]byte(content))

	_, err = pool.Exec(
		ctx,
		`INSERT INTO workspace_files(id, post_id, uploaded_by, name, size, sha256, content)
        VALUES($1,$2,$3,'private.txt',$4,$5,$6)`,
		fileID,
		post.ID,
		users[1],
		len(content),
		hex.EncodeToString(hash[:]),
		[]byte(content),
	)
	if err != nil {
		t.Fatal(err)
	}

	body := fmt.Sprintf(`{"id":%d,"action":"cancel"}`, post.ID)
	for _, user := range []int{1, 2} {
		if code, _ := request(user, http.MethodPost, "/posts/recover", body); code != 403 {
			t.Fatal("workers and outsiders must not cancel requester tasks")
		}
	}

	for range 2 {
		code, data := request(0, http.MethodPost, "/posts/recover", body)
		if result := decode(
			data,
		); code != 200 || result.Status != db.PostStatusCancelled ||
			result.AcceptedBy != post.AcceptedBy {
			t.Fatalf("cancel must be retry-safe and preserve participant history: %d %s", code, data)
		}
	}

	if code, _ := request(0, http.MethodPost, "/posts/fund", fmt.Sprintf(`{"id":%d}`, post.ID)); code != 409 {
		t.Fatal("canceled tasks must not prepare new funding")
	}

	if code, _ := request(
		1,
		http.MethodPost,
		fmt.Sprintf("/posts/%d/messages", post.ID),
		`{"text":"late message"}`,
	); code != 409 {
		t.Fatal("cancellation must close workspace writes")
	}

	var group sync.WaitGroup

	ids, failures := make(chan int64, 8), make(chan string, 8)

	for range 8 {
		group.Go(func() {
			code, data := request(
				0,
				http.MethodPost,
				"/posts/recover",
				fmt.Sprintf(`{"id":%d,"action":"reopen"}`, post.ID),
			)
			if code != 200 {
				failures <- string(data)
				return
			}

			ids <- decode(data).ID
		})
	}

	group.Wait()
	close(ids)
	close(failures)

	for failure := range failures {
		t.Fatal(failure)
	}

	var newID int64
	for id := range ids {
		if newID != 0 && newID != id {
			t.Fatal("concurrent reopen retries created duplicate tasks")
		}

		newID = id
	}

	reopened, err := c.queries.GetPost(ctx, newID)
	if err != nil || reopened.ID == post.ID || reopened.AcceptedBy.Valid || reopened.PosterWallet != "" ||
		reopened.WorkerWallet != "" || reopened.Description != post.Description || reopened.Status != db.PostStatusOpen {
		t.Fatalf("reopen must copy the exact brief into a fresh unaccepted task: %+v, %v", reopened, err)
	}

	source, err := c.queries.GetPost(ctx, post.ID)
	if err != nil || source.ReopenedAs.Int64 != newID {
		t.Fatal("the original task must retain its successor ID")
	}

	if code, _ := request(2, http.MethodPost, "/posts/accept", fmt.Sprintf(`{"id":%d}`, newID)); code != 200 {
		t.Fatal("a different worker must be able to claim the reopened task")
	}

	for _, test := range []struct {
		user int
		id   int64
		code int
	}{{0, post.ID, 200}, {1, post.ID, 200}, {2, post.ID, 403}, {1, newID, 403}, {2, newID, 200}} {
		code, data := request(test.user, http.MethodGet, fmt.Sprintf("/posts/%d/workspace", test.id), "")
		if code != test.code || (test.id == newID && strings.Contains(string(data), "private.txt")) {
			t.Fatalf("reopening leaked private workspace history: %d %s", code, data)
		}
	}

	if code, _ := request(
		2,
		http.MethodGet,
		fmt.Sprintf("/posts/%d/files/%s", post.ID, fileID),
		"",
	); code != 403 {
		t.Fatal("new workers must not download old workspace files")
	}

	for _, state := range []string{"prepared", "pending", "confirmed", "released", "refunded", "failed", "unavailable", "unimplemented", "expired"} {
		post := createAccepted()

		_, err := c.queries.SavePostEscrow(
			ctx,
			db.SavePostEscrowParams{PostID: post.ID, Address: uuid.NewString(),
				ProgramID: "program", Reviewer: "reviewer", Network: "localnet", Transaction: "plan",
				LastValidBlockHeight: 100, AgreementVersion: 2},
		)
		if err != nil {
			t.Fatal(err)
		}

		rpc.state.Store(state)

		if state == "confirmed" {
			if err := c.queries.UpdateEscrowState(
				ctx,
				db.UpdateEscrowStateParams{PostID: post.ID, State: "expired"},
			); err != nil {
				t.Fatal(err)
			}
		}

		code, data := request(
			0,
			http.MethodPost,
			"/posts/recover",
			fmt.Sprintf(`{"id":%d,"action":"cancel"}`, post.ID),
		)

		expected := 409
		if state == "unavailable" || state == "unimplemented" {
			expected = 503
		}

		if state == "expired" {
			expected = 200
		}

		if code != expected {
			t.Fatalf("chain state %s must produce %d: %d %s", state, expected, code, data)
		}

		saved, err := c.queries.GetPost(ctx, post.ID)
		if err != nil || (state != "expired" && saved.Status != db.PostStatusNegotiating) {
			t.Fatal("blocked recovery must leave the task untouched")
		}
	}

	post = createAccepted()
	if _, err := pool.Exec(
		ctx,
		"UPDATE posts SET end_time = NOW() - INTERVAL '1 hour' WHERE id = $1",
		post.ID,
	); err != nil {
		t.Fatal(err)
	}

	if code, _ := request(
		0,
		http.MethodPost,
		"/posts/recover",
		fmt.Sprintf(`{"id":%d,"action":"reopen"}`, post.ID),
	); code != 400 {
		t.Fatal("expired acceptance cutoffs require an explicit future replacement")
	}

	if saved, err := c.queries.GetPost(ctx, post.ID); err != nil || saved.Status != db.PostStatusNegotiating {
		t.Fatal("an invalid reopening cutoff must not cancel the original task")
	}

	deadline := time.Now().Add(2 * time.Hour).UTC().Format(time.RFC3339)
	if code, _ := request(
		0,
		http.MethodPost,
		"/posts/recover",
		fmt.Sprintf(`{"id":%d,"action":"reopen","end_time":%q}`, post.ID, deadline),
	); code != 200 {
		t.Fatal("an explicit future acceptance cutoff must allow reopening")
	}

	rpc.state.Store("prepared")

	for range 10 {
		post := createAccepted()
		results := make(chan [2]int, 2)

		group.Go(func() {
			code, _ := request(0, http.MethodPost, "/posts/fund", fmt.Sprintf(`{"id":%d}`, post.ID))
			results <- [2]int{0, code}
		})
		group.Go(func() {
			code, _ := request(
				0,
				http.MethodPost,
				"/posts/recover",
				fmt.Sprintf(`{"id":%d,"action":"cancel"}`, post.ID),
			)
			results <- [2]int{1, code}
		})
		group.Wait()
		close(results)

		var codes [2]int
		for result := range results {
			codes[result[0]] = result[1]
		}

		if codes != [2]int{200, 409} && codes != [2]int{409, 200} {
			t.Fatalf("funding and cancellation must never both succeed: %v", codes)
		}
	}
}
