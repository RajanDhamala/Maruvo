package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rajandhamala/Maruvo/cli/internal/api"
	"github.com/rajandhamala/Maruvo/cli/internal/auth"
)

func TestHumanControlCommandUsesOwnerProfile(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	mode := "agent"

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer owner-token" {
			t.Error("control did not use the owner's profile")
		}

		if r.URL.Path == "/me" {
			_ = json.NewEncoder(w).Encode(api.User{ID: "1", Username: "Human"})
			return
		}

		if r.URL.Path != "/posts/7/agent-control" {
			t.Errorf("unexpected route: %s", r.URL.Path)
			w.WriteHeader(404)

			return
		}

		if r.Method == http.MethodPut {
			var body struct {
				Mode string `json:"mode"`
			}
			if json.NewDecoder(r.Body).Decode(&body) != nil || body.Mode != "manual" {
				t.Error("control mode lost")
			}

			mode = body.Mode
		}

		_ = json.NewEncoder(w).Encode(api.AgentControl{PostID: 7, OwnerID: 1, Mode: mode})
	}))
	defer server.Close()

	if err := auth.SaveSession(server.URL, "owner-token", "human"); err != nil {
		t.Fatal(err)
	}

	for _, args := range [][]string{{"control", "--post", "7"}, {"control", "--post", "7", "--mode", "manual"}} {
		var out, log bytes.Buffer
		if err := Run(t.Context(), api.NewClient(server.URL), "human", args, &out, &log); err != nil {
			t.Fatal(err)
		}

		var control api.AgentControl
		if json.Unmarshal(out.Bytes(), &control) != nil || control.Mode != mode {
			t.Fatalf("invalid control output: %s", out.String())
		}
	}
}

func TestRunningHarnessContextStopsAfterHumanTakeover(t *testing.T) {
	var calls atomic.Int32

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mode := "agent"
		if calls.Add(1) > 1 {
			mode = "manual"
		}

		_ = json.NewEncoder(w).Encode(api.AgentControl{PostID: 7, OwnerID: 1, Mode: mode})
	}))
	defer server.Close()

	ctx, stop := context.WithTimeout(t.Context(), 10*time.Second)
	defer stop()

	runCtx, stopControl, err := controlledTaskContext(ctx, api.NewClient(server.URL), "owner", 7)
	if err != nil {
		t.Fatal(err)
	}
	defer stopControl()

	<-runCtx.Done()

	if !errors.Is(context.Cause(runCtx), errAgentPaused) {
		t.Fatalf("harness was not canceled on takeover: %v", context.Cause(runCtx))
	}
}
