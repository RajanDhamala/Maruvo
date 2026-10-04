package controller

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	db "github.com/rajandhamala/Maruvo/db/sqlc"
	middleware "github.com/rajandhamala/Maruvo/internal/middlewares"
	"github.com/rajandhamala/Maruvo/internal/utils"
	"github.com/rajandhamala/Maruvo/pb"
	"github.com/redis/go-redis/v9"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"
)

type deadlineRPC struct{ recoveryRPC }

func (r *deadlineRPC) CheckEscrow(context.Context, *pb.CheckEscrowRequest) (*pb.EscrowState, error) {
	return &pb.EscrowState{State: "confirmed"}, nil
}

func TestTaskDeadlineLifecycle(t *testing.T) {
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
	defer pool.Close()

	options, err := redis.ParseURL(redisURL)
	if err != nil {
		t.Fatal(err)
	}

	cache := redis.NewClient(options)
	defer cache.Close()

	listener := bufconn.Listen(1 << 20)
	serverRPC := grpc.NewServer()

	pb.RegisterSolanaServiceServer(serverRPC, &deadlineRPC{})

	go serverRPC.Serve(listener)
	defer serverRPC.Stop()

	connection, err := grpc.NewClient(
		"passthrough:///deadlines",
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return listener.Dial() }),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()

	c := NewController(pool, connection, nil, cache)

	users, tokens, wallets := make([]int64, 3), make([]string, 3), make([]string, 3)
	for i := range users {
		if err := pool.QueryRow(ctx, "INSERT INTO users(email,username) VALUES($1,'deadline fixture') RETURNING id",
			uuid.NewString()+"@example.test").
			Scan(&users[i]); err != nil {
			t.Fatal(err)
		}

		wallets[i] = uuid.NewString()
		if _, err := c.queries.LinkWallet(
			ctx,
			db.LinkWalletParams{UserID: users[i], Address: wallets[i]},
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
	mux.HandleFunc("POST /posts/create", middleware.Auth(c.CreatePost))
	mux.HandleFunc("POST /posts/accept", middleware.Auth(c.AcceptPost))
	mux.HandleFunc("POST /posts/fund", middleware.Auth(c.PreparePostFunding))
	mux.HandleFunc("POST /posts/recover", middleware.Auth(c.RecoverPost))
	mux.HandleFunc("POST /posts/urs", middleware.Auth(c.GetUrPosts))
	mux.HandleFunc("POST /posts/{id}/submit", middleware.Auth(c.SubmitWorkspace))
	mux.HandleFunc("POST /posts/{id}/review", middleware.Auth(c.RequestPostChanges))
	mux.HandleFunc("GET /posts/{id}/workspace", middleware.Auth(c.GetWorkspace))

	server := httptest.NewServer(mux)
	defer server.Close()

	request := func(user int, method, path string, payload any, want int) []byte {
		t.Helper()

		data, err := json.Marshal(payload)
		if err != nil {
			t.Fatal(err)
		}

		req, err := http.NewRequestWithContext(ctx, method, server.URL+path, strings.NewReader(string(data)))
		if err != nil {
			t.Fatal(err)
		}

		req.Header.Set("Authorization", "Bearer "+tokens[user])

		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()

		data, err = io.ReadAll(resp.Body)
		if err != nil || resp.StatusCode != want {
			t.Fatalf("%s: status=%d want=%d: %s %v", path, resp.StatusCode, want, data, err)
		}

		return data
	}
	decodePost := func(data []byte) db.Post {
		t.Helper()

		var response struct {
			Post db.Post `json:"post"`
		}
		if err := json.Unmarshal(data, &response); err != nil {
			t.Fatal(err)
		}

		return response.Post
	}
	accept := time.Now().Add(time.Hour).UTC().Truncate(time.Microsecond)
	deliver := accept.Add(24 * time.Hour)
	brief := CreatePostPayload{
		Title:                "Deadline task",
		Description:          "Return a clear text result",
		CostLamports:         1000000,
		EndTime:              accept,
		Level:                db.PostLevelEasy,
		FundingWindowSeconds: 60,
		DeliverBy:            deliver,
		ReviewWindowSeconds:  60,
	}
	invalid := brief
	invalid.DeliverBy = accept.Add(time.Minute)
	request(0, http.MethodPost, "/posts/create", invalid, 400)

	post := decodePost(request(0, http.MethodPost, "/posts/create", brief, 201))
	if post.FundBy.Valid || !post.DeliverBy.Time.Equal(deliver) {
		t.Fatal("creation must store explicit terms without starting the funding clock")
	}

	post = decodePost(request(1, http.MethodPost, "/posts/accept", map[string]int64{"id": post.ID}, 200))
	if post.FundBy.Time.Sub(post.AcceptedAt.Time) != time.Minute {
		t.Fatal("funding clock must begin at atomic acceptance")
	}

	quoteData := request(0, http.MethodPost, "/posts/fund", map[string]int64{"id": post.ID}, 200)

	var quote struct {
		Escrow fundingView `json:"escrow"`
	}
	if err := json.Unmarshal(quoteData, &quote); err != nil || quote.Escrow.AgreementVersion != 3 {
		t.Fatal("new timed tasks require v3 funding terms")
	}

	if _, err := pool.Exec(ctx, "DELETE FROM post_escrows WHERE post_id=$1", post.ID); err != nil {
		t.Fatal(err)
	}
	// Move fixture timestamps together to exercise overdue phases without waiting a day.
	if _, err := pool.Exec(
		ctx,
		`UPDATE posts SET end_time=end_time-INTERVAL '7 days', accepted_at=accepted_at-INTERVAL '7 days',
        fund_by=fund_by-INTERVAL '7 days', deliver_by=deliver_by-INTERVAL '7 days' WHERE id=$1`,
		post.ID,
	); err != nil {
		t.Fatal(err)
	}

	post, err = c.queries.GetPost(ctx, post.ID)
	if err != nil {
		t.Fatal(err)
	}

	notify := func(stage string, version int64) {
		t.Helper()

		var group sync.WaitGroup
		for range 8 {
			group.Go(func() {
				if err := c.notifyTaskDeadline(ctx, post.ID); err != nil {
					t.Error(err)
				}
			})
		}

		group.Wait()

		var count int
		if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM workspace_events WHERE post_id=$1 AND kind='task.overdue'
            AND data->>'stage'=$2 AND (data->>'submission_version')::bigint=$3`, post.ID, stage, version).
			Scan(&count); err != nil ||
			count != 1 {
			t.Fatalf("%s v%d must publish exactly one notice: %d %v", stage, version, count, err)
		}
	}
	notify("fund", 0)

	if saved, err := c.queries.GetPost(ctx, post.ID); err != nil || saved.Status != db.PostStatusNegotiating {
		t.Fatal("funding lateness must not cancel the task")
	}

	request(
		0,
		http.MethodPost,
		"/posts/recover",
		map[string]any{"id": post.ID, "action": "reopen", "end_time": accept},
		400,
	)

	if saved, _ := c.queries.GetPost(ctx, post.ID); saved.Status != db.PostStatusNegotiating {
		t.Fatal("invalid replacement timing must preserve the source task")
	}

	if _, err := c.queries.SavePostEscrow(
		ctx,
		db.SavePostEscrowParams{
			PostID:           post.ID,
			Address:          uuid.NewString(),
			ProgramID:        "program",
			Reviewer:         wallets[2],
			Network:          "localnet",
			Transaction:      "fixture",
			AgreementVersion: 3,
		},
	); err != nil {
		t.Fatal(err)
	}

	if err := c.queries.UpdateEscrowState(
		ctx,
		db.UpdateEscrowStateParams{PostID: post.ID, State: "confirmed"},
	); err != nil {
		t.Fatal(err)
	}

	if _, err := c.queries.MarkPostFunded(ctx, post.ID); err != nil {
		t.Fatal(err)
	}

	notify("deliver", 0)
	request(1, http.MethodPost, fmt.Sprintf("/posts/%d/submit", post.ID),
		map[string]any{"note": "Late but valid delivery", "submission_version": 0}, 201)

	workspace, err := c.queries.GetWorkspace(ctx, post.ID)
	if err != nil || workspace.ReviewBy.Time.Sub(workspace.SubmittedAt.Time) != time.Minute ||
		workspace.SubmissionVersion != 1 {
		t.Fatal("late delivery must remain allowed and start a fresh review window")
	}

	if _, err := pool.Exec(
		ctx,
		"UPDATE post_workspaces SET review_by=NOW()-INTERVAL '1 second' WHERE post_id=$1",
		post.ID,
	); err != nil {
		t.Fatal(err)
	}

	notify("review", 1)
	request(
		1,
		http.MethodPost,
		fmt.Sprintf("/posts/%d/review", post.ID),
		map[string]any{"note": "Revise", "submission_version": 1},
		403,
	)
	request(
		2,
		http.MethodPost,
		fmt.Sprintf("/posts/%d/review", post.ID),
		map[string]any{"note": "Revise", "submission_version": 1},
		201,
	)

	workspace, err = c.queries.GetWorkspace(ctx, post.ID)
	if err != nil || workspace.ReviewBy.Valid {
		t.Fatal("requesting changes must close the previous review window")
	}

	if current, err := c.queries.GetPost(
		ctx,
		post.ID,
	); err != nil ||
		!current.DeliverBy.Time.Equal(post.DeliverBy.Time) {
		t.Fatal("changes requested must never silently extend delivery terms")
	}

	request(
		1,
		http.MethodPost,
		fmt.Sprintf("/posts/%d/submit", post.ID),
		map[string]any{"note": "Revised result", "submission_version": 1},
		201,
	)

	workspace, err = c.queries.GetWorkspace(ctx, post.ID)
	if err != nil || workspace.SubmissionVersion != 2 || !workspace.ReviewBy.Valid {
		t.Fatal("revision delivery must start its own review window")
	}

	if _, err := pool.Exec(
		ctx,
		"UPDATE post_workspaces SET review_by=NOW()-INTERVAL '1 second' WHERE post_id=$1",
		post.ID,
	); err != nil {
		t.Fatal(err)
	}

	notify("review", 2)

	if _, err := c.queries.GetPostSettlement(ctx, post.ID); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatal("deadlines must not create settlement plans")
	}

	if escrow, err := c.queries.GetPostEscrow(ctx, post.ID); err != nil || escrow.State != "confirmed" {
		t.Fatal("overdue notifications must not move funds")
	}

	data := request(0, http.MethodGet, fmt.Sprintf("/posts/%d/workspace", post.ID), nil, 200)

	var view struct {
		Post publicPost `json:"post"`
	}
	if err := json.Unmarshal(
		data,
		&view,
	); err != nil || view.Post.Deadline.Stage != "review" || !view.Post.Deadline.Overdue ||
		!view.Post.Deadline.DeliveryLate {
		t.Fatal("participants must see review lateness and late-delivery history")
	}

	if err := c.queries.CloseWorkspaceReview(
		ctx,
		db.CloseWorkspaceReviewParams{PostID: post.ID, ReviewState: "approved"},
	); err != nil {
		t.Fatal(err)
	}

	if _, err := c.queries.MarkPostSettled(
		ctx,
		db.MarkPostSettledParams{ID: post.ID, Status: db.PostStatusCompleted},
	); err != nil {
		t.Fatal(err)
	}

	if err := c.notifyTaskDeadline(ctx, post.ID); err != nil {
		t.Fatal(err)
	}

	var total int
	if err := pool.QueryRow(ctx, "SELECT COUNT(*) FROM task_deadline_notices WHERE post_id=$1", post.ID).
		Scan(&total); err != nil ||
		total != 4 {
		t.Fatal("completed tasks must not publish further overdue notices")
	}

	legacy := brief
	legacy.DeliverBy, legacy.FundingWindowSeconds, legacy.ReviewWindowSeconds = time.Time{}, 0, 0
	old := decodePost(request(0, http.MethodPost, "/posts/create", legacy, 201))

	old = decodePost(request(1, http.MethodPost, "/posts/accept", map[string]int64{"id": old.ID}, 200))
	if old.FundBy.Valid || old.DeliverBy.Valid {
		t.Fatal("legacy clients must not acquire invented deadlines")
	}

	quoteData = request(0, http.MethodPost, "/posts/fund", map[string]int64{"id": old.ID}, 200)
	if err := json.Unmarshal(quoteData, &quote); err != nil || quote.Escrow.AgreementVersion != 2 {
		t.Fatal("untimed task funding must preserve the existing v2 flow")
	}

	newPost := decodePost(request(0, http.MethodPost, "/posts/create", brief, 201))
	newPost = decodePost(
		request(1, http.MethodPost, "/posts/accept", map[string]int64{"id": newPost.ID}, 200),
	)

	successor := decodePost(
		request(0, http.MethodPost, "/posts/recover", map[string]any{"id": newPost.ID, "action": "reopen",
			"end_time": accept.Add(time.Hour), "deliver_by": deliver.Add(time.Hour)}, 200),
	)
	if successor.AcceptedBy.Valid || successor.FundBy.Valid ||
		!successor.DeliverBy.Time.Equal(deliver.Add(time.Hour)) ||
		successor.FundingWindowSeconds != 60 ||
		successor.ReviewWindowSeconds != 60 {
		t.Fatal("reopening must preserve explicit windows and use fresh assignment clocks")
	}

	monitorCtx, stopMonitor := context.WithCancel(ctx)
	stopMonitor()

	done := make(chan struct{})

	go func() { c.RunDeadlineMonitor(monitorCtx); close(done) }()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("deadline monitor must stop cleanly")
	}
}
