package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/rajandhamala/Maruvo/cli/internal/api"
	"github.com/rajandhamala/Maruvo/cli/internal/auth"
)

func waitServer(t *testing.T, terminal bool, stream http.HandlerFunc) *httptest.Server {
	t.Helper()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer worker-token" {
			t.Error("wait did not authenticate its requests")
		}

		switch r.URL.Path {
		case "/posts/7/workspace":
			_ = json.NewEncoder(w).Encode(api.Workspace{
				Post: api.Post{ID: 7, Status: "in_progress"}, Cursor: "100-0",
				Context: api.TaskContext{Terminal: terminal},
			})
		case "/ws":
			stream(w, r)
		default:
			t.Errorf("unexpected wait request: %s", r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	t.Cleanup(server.Close)

	return server
}

func waitStream(
	t *testing.T,
	w http.ResponseWriter,
	r *http.Request,
	cursor string,
	events ...api.WorkspaceEvent,
) {
	t.Helper()

	upgrader := websocket.Upgrader{}

	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		t.Error(err)
		return
	}
	defer conn.Close()

	_ = conn.WriteJSON(map[string]any{"event": "connected", "data": map[string]string{"cursor": cursor}})
	for _, event := range events {
		if err := conn.WriteJSON(map[string]any{"event": "workspace.event", "data": event}); err != nil {
			t.Error(err)
			return
		}
	}

	_, _, _ = conn.ReadMessage()
}

func TestWaitCursorAndFilters(t *testing.T) {
	for _, test := range []struct {
		name       string
		cursor     string
		after      int64
		kinds      []string
		events     []api.WorkspaceEvent
		status     string
		wantCursor string
	}{
		{"snapshot cursor", "", 0, nil,
			[]api.WorkspaceEvent{{StreamID: "100-1", Kind: "message"}}, "event", "100-1"},
		{"replay with filter", "90-0", 0, []string{"work.submitted"},
			[]api.WorkspaceEvent{{StreamID: "90-1", Kind: "message"}, {StreamID: "100-2", Kind: "work.submitted"}},
			"event", "100-2"},
		{"filtered timeout advances cursor", "90-0", 0, []string{"work.submitted"},
			[]api.WorkspaceEvent{{StreamID: "100-1", Kind: "message"}}, "timeout", "100-1"},
		{"closure overrides filter", "90-0", 0, []string{"message"},
			[]api.WorkspaceEvent{{StreamID: "100-3", Kind: "task.status", Data: json.RawMessage(`{"status":"cancelled"}`)}},
			"closed", "100-3"},
		{"archived event cursor", "", 8, nil,
			[]api.WorkspaceEvent{{StreamID: "100-1", Kind: "message"}}, "event", "100-1"},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := waitServer(t, false, func(w http.ResponseWriter, r *http.Request) {
				cursor := test.cursor
				if cursor == "" && test.after == 0 {
					cursor = "100-0"
				}

				if r.URL.Query().Get("cursor") != cursor || r.URL.Query().Get("post_id") != "7" {
					t.Errorf("wait lost its resume cursor: %s", r.URL.RawQuery)
				}

				if test.after != 0 && r.URL.Query().Get("after") != "8" {
					t.Error("wait lost archived cursor")
				}

				if cursor == "" {
					cursor = "100-0"
				}

				waitStream(t, w, r, cursor, test.events...)
			})

			result, err := waitForTask(context.Background(), api.NewClient(server.URL), "worker-token", 7,
				test.after, test.cursor, test.kinds, 300*time.Millisecond)
			if err != nil || result.Status != test.status || result.Cursor != test.wantCursor ||
				result.PostID != 7 {
				t.Fatalf("wrong wait result: %+v (%v)", result, err)
			}

			if (result.Event != nil) != (test.status != "timeout") {
				t.Fatal("wait event shape disagrees with status")
			}
		})
	}
}

func TestWaitClosureResyncAndAuth(t *testing.T) {
	t.Run("already closed", func(t *testing.T) {
		server := waitServer(
			t,
			true,
			func(w http.ResponseWriter, r *http.Request) { t.Error("closed task opened stream") },
		)

		result, err := waitForTask(
			context.Background(),
			api.NewClient(server.URL),
			"worker-token",
			7,
			0,
			"90-0",
			nil,
			time.Second,
		)
		if err != nil || result.Status != "closed" || result.Cursor != "100-0" || result.Event != nil {
			t.Fatalf("closed task result: %+v %v", result, err)
		}
	})
	t.Run("reset stream requires task reload", func(t *testing.T) {
		var connections atomic.Int64

		server := waitServer(t, false, func(w http.ResponseWriter, r *http.Request) {
			connections.Add(1)

			if r.URL.Query().Get("cursor") == "999-0" {
				w.WriteHeader(409)
				return
			}

			waitStream(t, w, r, "100-0")
		})

		result, err := waitForTask(
			context.Background(),
			api.NewClient(server.URL),
			"worker-token",
			7,
			0,
			"999-0",
			nil,
			time.Second,
		)
		if err != nil || result.Status != "resync" || result.Cursor != "100-0" || connections.Load() != 2 {
			t.Fatalf("stream reset was silently skipped: %+v %v", result, err)
		}
	})
	t.Run("authentication error is not a timeout", func(t *testing.T) {
		server := waitServer(t, false, func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(401) })
		_, err := waitForTask(
			context.Background(),
			api.NewClient(server.URL),
			"worker-token",
			7,
			0,
			"90-0",
			nil,
			time.Second,
		)

		var failure *api.Error
		if !errors.As(err, &failure) || failure.StatusCode != 401 {
			t.Fatalf("authorization error lost: %v", err)
		}
	})
	t.Run("parent cancellation", func(t *testing.T) {
		server := waitServer(
			t,
			false,
			func(w http.ResponseWriter, r *http.Request) { waitStream(t, w, r, "100-0") },
		)
		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		_, err := waitForTask(ctx, api.NewClient(server.URL), "worker-token", 7, 0, "", nil, time.Second)
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("cancellation became a normal timeout: %v", err)
		}
	})
}

func TestWaitCommandSingleJSON(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	server := waitServer(
		t,
		false,
		func(w http.ResponseWriter, r *http.Request) { waitStream(t, w, r, "100-0") },
	)
	if err := auth.SaveSession(server.URL, "worker-token", "worker"); err != nil {
		t.Fatal(err)
	}

	var out, log bytes.Buffer

	err := Run(
		context.Background(),
		api.NewClient(server.URL),
		"worker",
		[]string{"wait", "--post", "7", "--timeout", "100ms"},
		&out,
		&log,
	)

	var result waitResult

	decoder := json.NewDecoder(&out)
	if err != nil || decoder.Decode(&result) != nil || result.Status != "timeout" ||
		result.Cursor != "100-0" ||
		log.Len() != 0 {
		t.Fatalf("command did not produce a quiet JSON result: %+v %v", result, err)
	}

	var extra any
	if decoder.Decode(&extra) != io.EOF {
		t.Fatal("wait emitted more than one JSON value")
	}
}
