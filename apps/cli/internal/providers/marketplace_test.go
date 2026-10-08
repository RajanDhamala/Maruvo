package providers

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rajandhamala/Maruvo/cli/internal/api"
	"github.com/rajandhamala/Maruvo/cli/internal/auth"
)

func marketCall(name, arguments string) ToolCall {
	return ToolCall{ID: "call", Type: "function", Function: Function{Name: name, Arguments: arguments}}
}

func taskBrief(t *testing.T) string {
	t.Helper()

	data, err := json.Marshal(api.CreatePostPayload{
		Title:              "Fix sorting",
		Description:        "Sort records numerically",
		AcceptanceCriteria: "Tests cover 2 before 10",
		InputFiles:         []string{},
		ExpectedOutputs:    []string{},
		CostLamports:       100,
		Level:              "easy",
		EndTime:            time.Now().Add(time.Hour),
		DeliverBy:          time.Now().Add(72 * time.Hour),
	})
	if err != nil {
		t.Fatal(err)
	}

	return string(data)
}

func TestMarketplaceTwoProfiles(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	var (
		created       bool
		owner, worker string
	)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/posts/create":
			owner = r.Header.Get("Authorization")

			var payload api.CreatePostPayload
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				t.Error(err)
			}

			if payload.FundingWindowSeconds != 86400 || payload.ReviewWindowSeconds != 86400 ||
				payload.CostLamports != 100 {
				t.Errorf("wrong brief: %+v", payload)
			}

			created = true

			io.WriteString(w, `{"post":{"id":7,"status":"open"}}`)
		case "/posts/feed":
			if !created {
				t.Error("discovery preceded publication")
			}

			io.WriteString(
				w,
				`{"posts":[{"id":7,"title":"Fix sorting","description":"private long data","status":"open","level":"easy"}]}`,
			)
		case "/posts/accept":
			worker = r.Header.Get("Authorization")

			io.WriteString(w, `{"post":{"id":7,"accepted_by":2,"status":"negotiating"}}`)
		case "/posts/info":
			io.WriteString(w, `{"post":{"id":7,"accepted_by":2},"escrow":{"state":"unfunded"}}`)
		case "/posts/7/workspace":
			io.WriteString(
				w,
				`{"post":{"id":7,"accepted_by":2},"context":{"waiting_for":["funding"],"human_payment_required":true},"escrow":{"state":"unfunded","transaction":"opaque"},"events":[{"data":{"token":"not for provider"}}]}`,
			)
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
		}
	}))
	defer server.Close()

	client := api.NewClient(server.URL)
	for _, profile := range []string{"requester", "worker"} {
		if err := auth.SaveSession(server.URL, profile+"-token", profile); err != nil {
			t.Fatal(err)
		}
	}

	requester, err := LoadMarketplace(client, "requester")
	if err != nil {
		t.Fatal(err)
	}

	workerMarket, err := LoadMarketplace(client, "worker")
	if err != nil {
		t.Fatal(err)
	}

	if _, err := requester.execute(t.Context(), marketCall("create_post", taskBrief(t))); err != nil {
		t.Fatal(err)
	}

	feed, err := workerMarket.execute(t.Context(), marketCall("find_posts", `{"level":"easy"}`))
	if err != nil || !strings.Contains(feed, `"id":7`) || strings.Contains(feed, "private long data") {
		t.Fatalf("feed: %s %v", feed, err)
	}

	if _, err := workerMarket.execute(t.Context(), marketCall("accept_post", `{"post":7}`)); err != nil {
		t.Fatal(err)
	}

	task, err := workerMarket.execute(t.Context(), marketCall("get_task", `{"post":7}`))
	if err != nil || !strings.Contains(task, `"waiting_for":["funding"]`) ||
		strings.Contains(task, "opaque") ||
		strings.Contains(task, "not for provider") {
		t.Fatalf("task: %s %v", task, err)
	}

	if owner != "Bearer requester-token" || worker != "Bearer worker-token" {
		t.Fatalf("profile isolation: %q %q", owner, worker)
	}
}

func TestMarketplaceSearchBeforePagination(t *testing.T) {
	var searched []string

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		posts := []api.Post{{ID: 40, Title: "Unrelated task"}}

		switch r.URL.Path {
		case "/posts/feed":
			var args struct {
				Level string `json:"level"`
			}
			if err := json.NewDecoder(r.Body).Decode(&args); err != nil {
				t.Error(err)
			}

			searched = append(searched, args.Level)
			switch args.Level {
			case "easy":
				posts = append(posts, api.Post{ID: 10, Title: "WebSocket reconnect"})
			case "medium":
				posts = append(
					posts,
					api.Post{ID: 20, Title: "Fix transport", Description: "Socket failures"},
				)
			case "complex":
				posts = append(
					posts,
					api.Post{ID: 30, Title: "Recovery", AcceptanceCriteria: "Socket tests pass"},
				)
			default:
				t.Errorf("unexpected level: %q", args.Level)
			}
		case "/posts/urs":
			posts = append(posts, api.Post{ID: 50, Title: "My Socket task", Status: "completed"})
		default:
			t.Errorf("unexpected path: %s", r.URL.Path)
		}

		json.NewEncoder(w).Encode(map[string]any{"posts": posts})
	}))
	defer server.Close()

	market := &Marketplace{client: api.NewClient(server.URL), token: "fake-login"}

	data, err := market.execute(
		t.Context(),
		marketCall("find_posts", `{"query":"  SOCKET  ","offset":1,"limit":1}`),
	)
	if err != nil || !strings.Contains(data, `"id":20`) || !strings.Contains(data, `"total":3`) ||
		!strings.Contains(
			data,
			`"has_more":true`,
		) || strings.Contains(data, `"id":40`) || len(searched) != 3 {
		t.Fatalf("all-level search and pagination: %s %v, searched %v", data, err, searched)
	}

	searched = nil

	data, err = market.execute(t.Context(), marketCall("find_posts", `{"query":"missing","level":"easy"}`))
	if err != nil || !strings.Contains(data, `"posts":[]`) || len(searched) != 1 || searched[0] != "easy" {
		t.Fatalf("single-level empty search: %s %v, searched %v", data, err, searched)
	}

	data, err = market.execute(t.Context(), marketCall("my_posts", `{"query":"socket"}`))
	if err != nil || !strings.Contains(data, `"id":50`) || !strings.Contains(data, `"total":1`) {
		t.Fatalf("own-post search: %s %v", data, err)
	}
}

func TestMarketplaceCredentialBoundaries(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	client := api.NewClient("http://127.0.0.1:3000")

	loggedOut, err := LoadMarketplace(client, "empty")
	if err != nil || loggedOut != nil || len(loggedOut.tools()) != 0 {
		t.Fatalf("logged out: %v %v", loggedOut, err)
	}

	if err := auth.SaveSession(client.URL(), "owner-token", "owner"); err != nil {
		t.Fatal(err)
	}

	file := filepath.Join(t.TempDir(), "credential.json")
	t.Setenv("MARUVO_AGENT_TOKEN_FILE", file)

	if _, err := LoadMarketplace(client, "owner"); err == nil {
		t.Fatal("missing grant fell back to owner")
	}

	if err := os.WriteFile(
		file,
		[]byte(`{"api_url":"http://127.0.0.1:3000","token":"mru_agent_scoped"}`),
		0600,
	); err != nil {
		t.Fatal(err)
	}

	scoped, err := LoadMarketplace(client, "owner")
	if err != nil || scoped.token != "mru_agent_scoped" || !scoped.scoped {
		t.Fatalf("scoped: %v %v", scoped, err)
	}

	if scoped.handles("create_post") || scoped.handles("find_agents") || !scoped.handles("get_workspace") ||
		!scoped.handles("send_message") {
		t.Fatal("grant exposed owner mutations")
	}

	for _, name := range []string{"create_post", "accept_post", "publish_offer", "find_agents", "fund", "settle", "send_message"} {
		if _, err := scoped.execute(t.Context(), marketCall(name, `{}`)); err == nil {
			t.Fatalf("grant allowed %s", name)
		}
	}

	root, err := os.OpenRoot(filepath.Dir(file))
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()

	if err := protectedAgentPath(
		root,
		marketCall("read_file", `{"path":"credential.json"}`),
		scoped,
	); err == nil {
		t.Fatal("grant file readable")
	}

	alias := filepath.Join(t.TempDir(), "project-alias")
	if err := os.Symlink(filepath.Dir(file), alias); err != nil {
		t.Fatal(err)
	}

	aliasRoot, err := os.OpenRoot(alias)
	if err != nil {
		t.Fatal(err)
	}
	defer aliasRoot.Close()

	if err := protectedAgentPath(
		aliasRoot,
		marketCall("read_file", `{"path":"credential.json"}`),
		scoped,
	); err == nil {
		t.Fatal("grant file readable through project alias")
	}
}

func TestMarketplaceSellerTools(t *testing.T) {
	terms := api.OfferTerms{Name: "Go fixes", Description: "Fix small Go bugs", Capabilities: []string{"Go"},
		MinLamports: 100, JobTimeoutSeconds: 600}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer seller-secret" {
			t.Error("seller tools used another profile")
		}

		switch r.URL.Path {
		case "/agent-offers/mine":
			var saved api.OfferTerms
			if r.Method != http.MethodPut || json.NewDecoder(r.Body).Decode(&saved) != nil ||
				saved.Name != terms.Name || saved.MinLamports != terms.MinLamports || saved.JobTimeoutSeconds != terms.JobTimeoutSeconds {
				t.Errorf("wrong offer: %+v", saved)
			}

			json.NewEncoder(w).Encode(api.AgentOffer{OfferTerms: terms, UserID: 2})
		case "/agent-offers":
			json.NewEncoder(w).Encode([]api.AgentOffer{{OfferTerms: terms, UserID: 2, Available: true}})
		default:
			t.Errorf("unexpected seller action: %s", r.URL.Path)
		}
	}))
	defer server.Close()

	market := &Marketplace{client: api.NewClient(server.URL), token: "seller-secret"}

	arguments, _ := json.Marshal(terms)
	for _, call := range []ToolCall{marketCall("publish_offer", string(arguments)), marketCall("find_agents", `{}`)} {
		data, err := market.execute(t.Context(), call)
		if err != nil || !strings.Contains(data, `"name":"Go fixes"`) ||
			strings.Contains(data, "seller-secret") {
			t.Fatalf("seller tool: %s %v", data, err)
		}
	}
}

func TestMarketplaceInvalidArgumentsAndFailures(t *testing.T) {
	var requests int

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests++

		w.WriteHeader(http.StatusForbidden)
		io.WriteString(w, `{"error":"grant expired token-secret"}`)
	}))
	defer server.Close()

	market := &Marketplace{client: api.NewClient(server.URL), token: "token-secret"}
	for _, call := range []ToolCall{
		marketCall("create_post", `{}`), marketCall("create_post", `{"title":"only a title"}`),
		marketCall("accept_post", `{"post":0}`), marketCall("accept_post", `{"post":7,"token":"evil"}`),
		marketCall("accept_post", `{"post":7} {}`), marketCall("find_posts", `{"level":"extreme"}`),
		marketCall("my_posts", `{"offset":-1}`), marketCall("find_posts", `{"level":"easy","limit":51}`),
		marketCall("find_posts", `{"query":"`+strings.Repeat("x", 201)+`"}`),
		marketCall("my_posts", `{"level":"easy"}`),
	} {
		if _, err := market.execute(t.Context(), call); err == nil {
			t.Fatalf("accepted invalid arguments: %+v", call)
		}
	}

	if requests != 0 {
		t.Fatal("invalid input reached API")
	}

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	if _, err := market.execute(
		ctx,
		marketCall("accept_post", `{"post":7}`),
	); !errors.Is(
		err,
		context.Canceled,
	) {
		t.Fatal(err)
	}

	_, err := market.execute(t.Context(), marketCall("accept_post", `{"post":7}`))
	if err == nil || requests != 1 || strings.Contains(market.redact(err.Error()), "token-secret") {
		t.Fatalf("failure: %v", err)
	}
}

func TestMarketplaceAgentLoopRedactionAndDuplicateMutation(t *testing.T) {
	var mutations int

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		mutations++

		w.WriteHeader(http.StatusConflict)
		io.WriteString(w, `{"error":"already accepted owner-token"}`)
	}))
	defer server.Close()

	market := &Marketplace{client: api.NewClient(server.URL), token: "owner-token"}

	var steps int

	client := mockClient(t, "deepseek", func(w http.ResponseWriter, r *http.Request) {
		data, _ := io.ReadAll(r.Body)
		if strings.Contains(string(data), "owner-token") {
			t.Error("login token sent to model")
		}

		var request struct {
			Messages []Message `json:"messages"`
			Tools    []Tool    `json:"tools"`
		}
		if err := json.Unmarshal(data, &request); err != nil {
			t.Error(err)
		}

		if len(request.Tools) != 18 {
			t.Errorf("expected local and marketplace tools, got %d", len(request.Tools))
		}

		steps++
		if steps <= 2 {
			arguments := `{"post":7}`
			if steps == 2 {
				arguments = `{ "post" : 7 }`
			}

			json.NewEncoder(w).
				Encode(map[string]any{"choices": []any{map[string]any{"message": Message{Role: "assistant", ReasoningContent: "owner-token", ToolCalls: []ToolCall{marketCall("accept_post", arguments)}}}}})
		} else {
			if !strings.Contains(string(data), "already attempted") {
				t.Error("repeat wasn't blocked")
			}

			io.WriteString(
				w,
				`{"choices":[{"message":{"role":"assistant","content":"Acceptance failed owner-token"}}]}`,
			)
		}
	})

	var events []Event

	history, err := client.RunAgent(
		t.Context(),
		t.TempDir(),
		nil,
		"Accept post 7",
		nil,
		func(event Event) { events = append(events, event) },
		market,
	)
	if err != nil || mutations != 1 {
		t.Fatalf("loop: %v, mutations %d", err, mutations)
	}

	data, _ := json.Marshal(struct {
		History []Message
		Events  []Event
	}{history, events})
	if strings.Contains(string(data), "owner-token") ||
		!strings.Contains(string(data), "login token redacted") {
		t.Fatalf("unsafe output: %s", data)
	}
}

func TestMarketplaceAPIRedirectDenied(t *testing.T) {
	reached := false

	target := httptest.NewServer(
		http.HandlerFunc(func(http.ResponseWriter, *http.Request) { reached = true }),
	)
	defer target.Close()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusTemporaryRedirect)
	}))
	defer server.Close()

	market := &Marketplace{client: api.NewClient(server.URL), token: "secret"}
	if _, err := market.execute(t.Context(), marketCall("accept_post", `{"post":7}`)); err == nil || reached {
		t.Fatal("redirect forwarded an authenticated mutation")
	}
}
