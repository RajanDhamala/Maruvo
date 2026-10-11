package agent

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/rajandhamala/Maruvo/cli/internal/api"
	"github.com/rajandhamala/Maruvo/cli/internal/auth"
)

func TestInboxHarnessHelper(t *testing.T) {
	if os.Getenv("MARUVO_INBOX_TEST") != "1" {
		return
	}

	if os.Getenv("MARUVO_INBOX_FAIL") == "1" {
		os.Exit(4)
	}

	var payload struct {
		Mode         string        `json:"mode"`
		Workspace    api.Workspace `json:"workspace"`
		CLIArguments []string      `json:"cli_arguments"`
	}
	if json.NewDecoder(os.Stdin).Decode(&payload) != nil || payload.Mode != "inbox" ||
		payload.Workspace.Post.ID != 7 ||
		len(payload.CLIArguments) < 2 ||
		os.Getenv("MARUVO_TOKEN") != "" {
		os.Exit(2)
	}

	token, scoped, err := auth.LoadAgentSession(payload.CLIArguments[1])
	if err != nil || !scoped || token != "mru_agent_inbox-test" {
		os.Exit(3)
	}

	if payload.Workspace.Post.Status != "completed" {
		client := api.NewClient(payload.CLIArguments[1])

		_, err = client.SendMessageReceipt(
			t.Context(),
			token,
			7,
			"Answer to the worker",
			"reply-"+payload.Workspace.Cursor,
		)
		if err != nil {
			os.Exit(3)
		}
	}

	os.Exit(0)
}

func TestInboxResumesWithoutReplyLoopAndRetriesFailedContinuation(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("MARUVO_INBOX_TEST", "1")
	t.Setenv("MARUVO_TOKEN", "owner-secret")

	owner, worker := int64(1), int64(2)
	workspace := api.Workspace{
		Post:   api.Post{ID: 7, UserID: owner, AcceptedBy: &worker, Status: "in_progress"},
		Cursor: "1-0",
		State:  api.WorkspaceState{ReviewState: "working", LastEventID: 1},
		Events: []api.WorkspaceEvent{{ID: 1, StreamID: "1-0", ActorID: &worker, Kind: "message"}},
	}

	var mu sync.Mutex

	grants, revoked, replies := 0, 0, 0

	var lastPermissions []string

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()

		encode := json.NewEncoder(w).Encode

		switch r.URL.Path {
		case "/posts/7/agent-control":
			_ = encode(api.AgentControl{PostID: 7, OwnerID: owner, Mode: "agent"})
		case "/agent/inbox":
			_ = encode(map[string]any{"posts": []api.Post{workspace.Post}})
		case "/posts/7/workspace":
			_ = encode(workspace)
		case "/posts/7/agents":
			if r.Header.Get("Authorization") != "Bearer owner-secret" {
				t.Error("grant did not use owner login")
			}

			var request struct {
				Permissions []string `json:"permissions"`
			}

			_ = json.NewDecoder(r.Body).Decode(&request)
			lastPermissions = request.Permissions
			grants++
			_ = encode(
				api.AgentCredential{
					APIURL: "http://" + r.Host,
					Token:  "mru_agent_inbox-test",
					Grant:  api.AgentGrant{ID: "inbox-grant", PostID: 7},
				},
			)
		case "/agents/inbox-grant/revoke":
			revoked++
			_ = encode(api.AgentGrant{ID: "inbox-grant"})
		case "/posts/7/messages":
			if r.Header.Get("Authorization") != "Bearer mru_agent_inbox-test" {
				t.Error("harness used owner credentials")
			}

			replies++
			workspace.State.LastEventID++
			workspace.Cursor = strconv.FormatInt(workspace.State.LastEventID, 10) + "-0"
			workspace.Events = append(
				workspace.Events,
				api.WorkspaceEvent{
					ID:       workspace.State.LastEventID,
					StreamID: workspace.Cursor,
					ActorID:  &owner,
					Kind:     "message",
				},
			)
			_ = encode(api.MessageReceipt{MessageID: "reply", StreamID: "2-0"})
		default:
			t.Errorf("unexpected path: %s", r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	defer server.Close()

	client := api.NewClient(server.URL)
	exe, _ := os.Executable()
	directory := t.TempDir()

	var out, log bytes.Buffer

	run := func() error {
		return listenInbox(
			t.Context(),
			client,
			"owner-secret",
			"requester",
			"1",
			directory,
			exe,
			[]string{"-test.run=^TestInboxHarnessHelper$"},
			true,
			&out,
			&log,
		)
	}
	if err := run(); err != nil {
		t.Fatalf("first callback: %v %s", err, log.String())
	}

	if err := run(); err != nil {
		t.Fatal(err)
	}

	if grants != 1 || replies != 1 || revoked != 1 {
		t.Fatalf("own reply triggered a loop: grants=%d replies=%d revoked=%d", grants, replies, revoked)
	}

	mu.Lock()
	workspace.Cursor = "3-0"
	workspace.State.LastEventID = 3
	workspace.Events = append(
		workspace.Events,
		api.WorkspaceEvent{ID: 3, StreamID: "3-0", ActorID: &worker, Kind: "message"},
	)
	mu.Unlock()

	mu.Lock()
	workspace.AgentControl.Mode = "manual"
	mu.Unlock()

	if err := run(); err != nil {
		t.Fatal(err)
	}

	var pausedCheckpoint inboxCheckpoint
	if err := auth.LoadRemoteState("requester", "inbox.json", &pausedCheckpoint); err != nil ||
		pausedCheckpoint.Tasks["7"].Cursor != "2-0" || grants != 1 || replies != 1 {
		t.Fatalf("manual takeover acknowledged or answered pending work: %+v %v", pausedCheckpoint, err)
	}

	mu.Lock()
	workspace.AgentControl.Mode = "agent"
	mu.Unlock()
	t.Setenv("MARUVO_INBOX_FAIL", "1")

	if run() == nil {
		t.Fatal("failed callback acknowledged")
	}

	var checkpoint inboxCheckpoint
	if err := auth.LoadRemoteState(
		"requester",
		"inbox.json",
		&checkpoint,
	); err != nil ||
		checkpoint.Tasks["7"].Cursor != "2-0" {
		t.Fatalf("failed callback advanced checkpoint: %+v %v", checkpoint, err)
	}

	t.Setenv("MARUVO_INBOX_FAIL", "0")

	if err := run(); err != nil {
		t.Fatal(err)
	}

	mu.Lock()
	workspace.Post.Status = "completed"
	workspace.State.ReviewState = "approved"
	workspace.Cursor = "5-0"
	mu.Unlock()

	if err := run(); err != nil {
		t.Fatal(err)
	}

	if strings.Join(lastPermissions, ",") != "read" || grants != 4 || revoked != 4 || replies != 2 {
		t.Fatalf(
			"resume/closed access: %v, grants=%d revoked=%d replies=%d",
			lastPermissions,
			grants,
			revoked,
			replies,
		)
	}
}

func TestInboxPhaseAndRemoteEvents(t *testing.T) {
	owner, worker := int64(1), int64(2)
	base := api.Workspace{
		Post:   api.Post{Status: "in_progress"},
		Cursor: "5-0",
		State:  api.WorkspaceState{ReviewState: "working", LastEventID: 5},
	}
	position, _ := inboxUpdate(base, inboxPosition{}, "1")
	base.Cursor = "6-0"
	base.State.LastEventID = 6
	base.Events = []api.WorkspaceEvent{{ID: 6, StreamID: "6-0", ActorID: &owner}}

	next, pending := inboxUpdate(base, position, "1")
	if pending {
		t.Fatal("own event woke harness")
	}

	base.Events[0] = api.WorkspaceEvent{ID: 7, StreamID: "5-0", ActorID: &worker}
	if _, pending = inboxUpdate(base, position, "1"); pending {
		t.Fatal("archival of an acknowledged Redis message woke harness")
	}

	base.Events[0] = api.WorkspaceEvent{ID: 5, StreamID: "6-0", ActorID: &worker}
	if _, pending = inboxUpdate(base, position, "1"); pending {
		t.Fatal("publication of an acknowledged SQL event woke harness")
	}

	base.Events[0] = api.WorkspaceEvent{ID: 6, StreamID: "6-0", ActorID: &worker}
	if _, pending = inboxUpdate(base, position, "1"); !pending {
		t.Fatal("remote event lost")
	}

	base.Events = nil
	base.State.ReviewState = "submitted"

	base.State.SubmissionVersion = 1
	if _, pending = inboxUpdate(base, next, "1"); !pending {
		t.Fatal("offline delivery lost")
	}
}

func TestPromptBridgeFeedsExistingHarnessAndStripsOwnerSecrets(t *testing.T) {
	t.Setenv("MARUVO_TOKEN", "owner-secret")

	var out bytes.Buffer

	payload := `{"mode":"execute","instructions":"Write the declared output","task":{"description":"Fix sorting"}}`
	if err := bridgeHarness(
		t.Context(),
		"/bin/cat",
		nil,
		strings.NewReader(payload),
		&out,
	); err != nil || !strings.Contains(out.String(), "Fix sorting") ||
		!strings.Contains(out.String(), "untrusted data") {
		t.Fatalf("bridge: %s %v", out.String(), err)
	}

	if bridgeHarness(
		t.Context(),
		"/bin/cat",
		nil,
		strings.NewReader(`{"mode":"arbitrary"}`),
		io.Discard,
	) == nil {
		t.Fatal("invalid mode reached harness")
	}

	out.Reset()

	if err := bridgeHarness(
		t.Context(),
		"/usr/bin/env",
		nil,
		strings.NewReader(payload),
		&out,
	); err != nil ||
		strings.Contains(out.String(), "MARUVO_TOKEN=") {
		t.Fatal("bridge passed an owner token", err)
	}
}
