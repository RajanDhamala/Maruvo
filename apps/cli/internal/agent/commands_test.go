package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/rajandhamala/Maruvo/cli/internal/api"
	"github.com/rajandhamala/Maruvo/cli/internal/auth"
)

func TestDeliveryCommands(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	var (
		payload map[string]any
		path    string
	)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer worker-token" {
			t.Error("command did not use its saved profile")
		}

		if r.URL.Path == "/me" {
			_, _ = w.Write([]byte(`{"id":"2","username":"worker","email":"worker@example.test"}`))
			return
		}

		path = r.URL.Path

		payload = nil
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Error(err)
		}

		_ = json.NewEncoder(w).Encode(map[string]any{
			"event_id": 10, "post_id": 7, "submission_version": 3, "review_state": "submitted",
		})
	}))
	defer server.Close()

	if err := auth.SaveSession(server.URL, "worker-token", "worker"); err != nil {
		t.Fatal(err)
	}

	run := func(args ...string) {
		t.Helper()

		var out, log bytes.Buffer
		if err := Run(
			context.Background(),
			api.NewClient(server.URL),
			"worker",
			args,
			&out,
			&log,
		); err != nil {
			t.Fatal(err)
		}

		var receipt api.WorkspaceReceipt
		if err := json.Unmarshal(
			out.Bytes(),
			&receipt,
		); err != nil || receipt.EventID != 10 ||
			receipt.SubmissionVersion != 3 {
			t.Fatalf("mutation receipt lost: %s (%v)", out.String(), err)
		}
	}
	run(
		"submit",
		"--post",
		"7",
		"--version",
		"2",
		"--text",
		"Ready",
		"--file-id",
		"patch",
		"--file-id",
		"tests",
		"--input-id",
		"source",
		"--input-id",
		"spec",
	)

	if path != "/posts/7/submit" || payload["submission_version"] != float64(2) {
		t.Fatalf("wrong submission request: %s %v", path, payload)
	}

	for key, want := range map[string][]string{"files": {"patch", "tests"}, "input_files": {"source", "spec"}} {
		got := payload[key].([]any)
		if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
			t.Fatalf("%s order changed: %v", key, got)
		}
	}

	run("submit", "--post", "7", "--version", "2", "--text", "No inputs")

	if inputs, ok := payload["input_files"].([]any); !ok || len(inputs) != 0 {
		t.Fatalf("empty input snapshot must be an array, not omitted/null: %v", payload)
	}

	run("submit", "--post", "7", "--text", "Legacy note")

	if _, present := payload["submission_version"]; present || len(payload) != 1 {
		t.Fatalf("legacy submission shape changed: %v", payload)
	}

	run("request-changes", "--post", "7", "--version", "3", "--text", "Add coverage")

	if path != "/posts/7/review/changes" || payload["submission_version"] != float64(3) {
		t.Fatalf("wrong revision request: %s %v", path, payload)
	}
}

func TestCreateBriefRejectsUnknownFields(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	var creates atomic.Int64

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/me" {
			_, _ = w.Write([]byte(`{"id":"1","username":"poster"}`))
			return
		}

		creates.Add(1)

		_, _ = w.Write([]byte(`{"post":{"id":7,"status":"open"}}`))
	}))
	defer server.Close()

	if err := auth.SaveSession(server.URL, "token", "poster"); err != nil {
		t.Fatal(err)
	}

	path := filepath.Join(t.TempDir(), "brief.json")
	for _, input := range []struct {
		body string
		want string
	}{
		{`{"title":"Repair","difficulty":"easy"}`, `unknown field "difficulty"`},
		{`{"title":"Repair","acceptance_criteria":["Pass tests"]}`, "cannot unmarshal array"},
		{`{"title":"Repair","level":"easy","acceptance_criteria":"Pass tests"}`, ""},
	} {
		if err := os.WriteFile(path, []byte(input.body), 0600); err != nil {
			t.Fatal(err)
		}

		var out, log bytes.Buffer

		err := Run(context.Background(), api.NewClient(server.URL), "poster",
			[]string{"create", "--brief", path}, &out, &log)
		if input.want == "" {
			if err != nil || creates.Load() != 1 {
				t.Fatalf("supported brief was not posted: %v", err)
			}
		} else if err == nil || !strings.Contains(err.Error(), input.want) || creates.Load() != 0 || out.Len() != 0 {
			t.Fatalf("invalid brief reached API or lost its field error: %v", err)
		}
	}
}

func TestInvalidDeliveryArgumentsBeforeLogin(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	for _, args := range [][]string{
		{"unknown"},
		{"submit", "--post", "1", "--file-id", "output"},
		{"submit", "--post", "1", "--version", "-2"},
		{"submit", "--post", "1", "--version", "oops"},
		{"submit", "--wat"},
		{"request-changes", "--post", "1", "--version", "0", "--text", "Fix"},
		{"request-changes", "--post", "1", "--version", "1"},
		{"wait", "--post", "1", "--timeout", "0s"},
		{"wait", "--post", "1", "--timeout", "6m"},
		{"wait", "--post", "1", "--after", "-1"},
		{"history", "--post", "1", "--before", "-1"},
		{"history", "--post", "1", "--limit", "0"},
		{"history", "--post", "1", "--limit", "101"},
	} {
		var out, log bytes.Buffer

		err := Run(context.Background(), api.NewClient("http://127.0.0.1:1"), "empty", args, &out, &log)

		var failure *commandError
		if !errors.As(err, &failure) || failure.Code != "INVALID_ARGUMENT" || out.Len() != 0 ||
			log.Len() != 0 {
			t.Fatalf(
				"%v: expected a quiet argument failure before auth, got %v stdout=%q stderr=%q",
				args,
				err,
				out.String(),
				log.String(),
			)
		}
	}
}

func TestHistoryCommand(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/me" {
			_, _ = w.Write([]byte(`{"id":"2","username":"worker"}`))
			return
		}

		if r.URL.Path != "/posts/7/history" || r.URL.Query().Get("before") != "80" ||
			r.URL.Query().Get("limit") != "25" || r.Header.Get("Authorization") != "Bearer worker-token" {
			t.Errorf("history continuation changed: %s", r.URL)
		}

		_, _ = w.Write([]byte(`{"post_id":7,"events":[{"id":79,"kind":"message","actor_id":2,
			"data":{"agent_grant_id":"grant","text":"older"}}],"has_more":true,"next_before":79}`))
	}))
	defer server.Close()

	if err := auth.SaveSession(server.URL, "worker-token", "worker"); err != nil {
		t.Fatal(err)
	}

	var out, log bytes.Buffer

	err := Run(context.Background(), api.NewClient(server.URL), "worker",
		[]string{"history", "--post", "7", "--before", "80", "--limit", "25"}, &out, &log)

	var page api.TaskHistory
	if err != nil || json.Unmarshal(out.Bytes(), &page) != nil || !page.HasMore || page.NextBefore != 79 ||
		len(page.Events) != 1 || page.Events[0].ActorID == nil || *page.Events[0].ActorID != 2 ||
		!bytes.Contains(page.Events[0].Data, []byte("grant")) || log.Len() != 0 {
		t.Fatalf("history command lost continuation or provenance: %s (%v)", out.String(), err)
	}
}
