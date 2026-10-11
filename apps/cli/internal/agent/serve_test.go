package agent

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rajandhamala/Maruvo/cli/internal/api"
	"github.com/rajandhamala/Maruvo/cli/internal/auth"
)

func TestSellerHarnessHelper(t *testing.T) {
	if os.Getenv("MARUVO_SELLER_TEST") != "1" {
		return
	}

	data, err := io.ReadAll(os.Stdin)
	if err != nil {
		os.Exit(2)
	}

	var task struct {
		Mode            string   `json:"mode"`
		SelectionPath   string   `json:"selection_path"`
		OutputDirectory string   `json:"output_directory"`
		CLIArguments    []string `json:"cli_arguments"`
	}
	if json.Unmarshal(data, &task) != nil {
		os.Exit(2)
	}

	if task.Mode == "select" {
		selection := os.Getenv("MARUVO_SELLER_SELECTION")
		if selection == "" {
			selection = `{"post_id":7}`
		}

		err = os.WriteFile(task.SelectionPath, []byte(selection), 0600)
	} else {
		if len(task.CLIArguments) < 2 {
			os.Exit(3)
		}

		token, scoped, loadErr := auth.LoadAgentSession(task.CLIArguments[1])
		if loadErr != nil || !scoped || token != "mru_agent_temporary-task-token" {
			os.Exit(3)
		}

		err = os.WriteFile(
			filepath.Join(task.OutputDirectory, "result.txt"),
			[]byte("Fixed the requested problem."),
			0600,
		)
	}

	if err != nil {
		os.Exit(2)
	}

	os.Exit(0)
}

func TestSellerSelectionMustUseAnOfferedTask(t *testing.T) {
	t.Setenv("MARUVO_SELLER_TEST", "1")

	exe, _ := os.Executable()

	for _, test := range []struct {
		selection string
		want      int64
		valid     bool
	}{
		{`{"post_id":7}`, 7, true}, {`{"post_id":0}`, 0, true},
		{`{"post_id":99}`, 0, false}, {`{"post_id":7,"price":1}`, 0, false},
		{`{"post_id":null}`, 0, false}, {`{"post_id":7} {}`, 0, false},
	} {
		t.Setenv("MARUVO_SELLER_SELECTION", test.selection)

		id, err := selectOfferTask(t.Context(), api.AgentOffer{}, []api.Post{{ID: 7}}, t.TempDir(), exe,
			[]string{"-test.run=^TestSellerHarnessHelper$"}, io.Discard)
		if (err == nil) != test.valid || id != test.want {
			t.Fatalf("%s: id=%d err=%v", test.selection, id, err)
		}
	}
}

func TestServeSelectsClaimsExecutesAndClearsAvailability(t *testing.T) {
	t.Setenv("MARUVO_SELLER_TEST", "1")
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	worker := int64(2)
	terms := api.OfferTerms{
		Name:              "Go worker",
		Description:       "Fix Go bugs",
		Capabilities:      []string{"Go"},
		MinLamports:       100,
		JobTimeoutSeconds: 60,
	}
	offer := api.AgentOffer{OfferTerms: terms, UserID: worker}
	post := api.Post{
		ID:           7,
		UserID:       1,
		Title:        "Repair",
		Description:  "Fix the requested problem",
		CostLamports: 100,
		Status:       "open",
		Level:        "easy",
		EndTime:      time.Now().Add(time.Hour),
	}

	var mu sync.Mutex

	claimed, submitted, released, revoked := false, false, false, false
	lease := ""

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()

		if r.Header.Get("Authorization") != "Bearer seller-session" {
			t.Error("wrong profile")
		}

		encode := json.NewEncoder(w).Encode

		switch r.URL.Path {
		case "/me":
			_ = encode(api.User{ID: "2", Username: "seller"})
		case "/wallet":
			_ = encode(map[string]string{"address": "worker-wallet"})
		case "/agent-offers/mine":
			_ = encode(offer)
		case "/agent-offers/mine/lease":
			var payload struct {
				Lease string `json:"lease"`
			}

			_ = json.NewDecoder(r.Body).Decode(&payload)
			if r.Method == http.MethodDelete {
				released = true

				if payload.Lease != lease {
					t.Error("wrong cleanup lease")
				}

				_ = encode(map[string]string{"status": "offline"})

				return
			}

			if lease != "" && lease != payload.Lease {
				t.Error("lease changed")
			}

			lease = payload.Lease
			_ = encode(offer)
		case "/posts/urs":
			posts := []api.Post{}
			if claimed {
				posts = append(posts, post)
			}

			_ = encode(map[string]any{"posts": posts})
		case "/posts/feed":
			var payload struct {
				Level string `json:"level"`
			}

			_ = json.NewDecoder(r.Body).Decode(&payload)

			posts := []api.Post{}
			if payload.Level == "easy" {
				posts = append(posts, post)
			}

			_ = encode(map[string]any{"posts": posts})
		case "/posts/accept":
			var payload struct {
				ID    int64  `json:"id"`
				Lease string `json:"offer_lease"`
			}

			_ = json.NewDecoder(r.Body).Decode(&payload)
			if payload.ID != 7 || payload.Lease != lease || lease == "" {
				t.Error("unbounded claim")
			}

			claimed = true
			post.AcceptedBy = &worker
			post.Status = "in_progress"
			_ = encode(map[string]any{"post": post})
		case "/posts/7/workspace":
			_ = encode(
				api.Workspace{
					Post:   post,
					Escrow: api.Escrow{State: "confirmed"},
					State:  api.WorkspaceState{ReviewState: "working"},
				},
			)
		case "/posts/7/agent-control":
			_ = encode(api.AgentControl{PostID: 7, OwnerID: worker, Mode: "agent"})
		case "/posts/7/messages":
			_ = encode(map[string]string{"stream_id": "1-0", "message_id": "started"})
		case "/posts/7/activity":
			var payload struct {
				RunID string `json:"run_id"`
				State string `json:"state"`
			}
			if json.NewDecoder(r.Body).Decode(&payload) != nil || len(payload.RunID) < 32 ||
				(payload.State != "working" && payload.State != "waiting_for_review") {
				t.Error("invalid runner activity")
			}

			_ = encode(map[string]string{"status": "reported"})
		case "/posts/7/agents":
			var grantRequest struct {
				Permissions []string `json:"permissions"`
				Lifetime    int64    `json:"expires_in_seconds"`
			}
			if json.NewDecoder(r.Body).Decode(&grantRequest) != nil ||
				strings.Join(grantRequest.Permissions, ",") != "read,message,upload,submit" ||
				grantRequest.Lifetime < 60 || grantRequest.Lifetime > terms.JobTimeoutSeconds {
				t.Errorf("unbounded task credential: %+v", grantRequest)
			}

			_ = encode(
				api.AgentCredential{
					APIURL: "http://" + r.Host,
					Token:  "mru_agent_temporary-task-token",
					Grant:  api.AgentGrant{ID: "temporary-grant", PostID: 7},
				},
			)
		case "/agents/temporary-grant/revoke":
			revoked = true
			_ = encode(api.AgentGrant{ID: "temporary-grant"})
		case "/posts/7/submit":
			var payload struct {
				Note       string   `json:"note"`
				Submission string   `json:"submission"`
				InputFiles []string `json:"input_files"`
				Version    int64    `json:"submission_version"`
			}

			_ = json.NewDecoder(r.Body).Decode(&payload)
			if payload.Note != "Fixed the requested problem." || payload.Version != 0 ||
				payload.InputFiles == nil {
				t.Errorf("delivery lost: %+v", payload)
			}

			submitted = true
			_ = encode(api.WorkspaceReceipt{PostID: 7, SubmissionVersion: 1, ReviewState: "submitted"})
		default:
			t.Errorf("unexpected request: %s", r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	defer server.Close()

	if err := auth.SaveSession(server.URL, "seller-session", "seller"); err != nil {
		t.Fatal(err)
	}

	exe, _ := os.Executable()

	var out, log bytes.Buffer

	directory := t.TempDir()

	err := Run(t.Context(), api.NewClient(server.URL), "seller", []string{"serve", "--exec", exe,
		"--arg", "-test.run=^TestSellerHarnessHelper$", "--dir", directory, "--once"}, &out, &log)
	if err != nil {
		t.Fatalf("serve: %v\n%s\n%s", err, out.String(), log.String())
	}

	mu.Lock()
	defer mu.Unlock()

	if !claimed || !submitted || !released || !revoked || strings.Contains(out.String(), lease) ||
		strings.Contains(out.String(), "seller-session") ||
		strings.Contains(out.String(), "temporary-task-token") {
		t.Fatalf("seller workflow incomplete or leaked credentials: %s", out.String())
	}

	if err := filepath.WalkDir(directory, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}

		if entry.Name() == ".agent-session.json" {
			t.Errorf("temporary credential retained: %s", path)
		}

		return nil
	}); err != nil {
		t.Fatal(err)
	}
}
