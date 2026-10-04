package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
)

func TestSendMessageRetriesLostResponse(t *testing.T) {
	var (
		ids []string
		mu  sync.Mutex
	)

	snapshot := func() []string {
		mu.Lock()
		defer mu.Unlock()

		return append([]string(nil), ids...)
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var message map[string]string
		if err := json.NewDecoder(r.Body).Decode(&message); err != nil {
			t.Error(err)
		}

		mu.Lock()

		ids = append(ids, message["message_id"])
		first := len(ids) == 1
		mu.Unlock()

		if first {
			conn, _, err := w.(http.Hijacker).Hijack()
			if err != nil {
				t.Error(err)
				return
			}

			conn.Close()

			return
		}

		w.WriteHeader(http.StatusCreated)
	}))
	defer server.Close()

	client := NewClient(server.URL)
	if err := client.SendMessage(context.Background(), "token", 1, "hello"); err != nil {
		t.Fatal(err)
	}

	got := snapshot()
	if len(got) != 2 || got[0] == "" || got[0] != got[1] {
		t.Fatalf("lost-response retry did not preserve the message ID: %v", got)
	}

	if err := client.SendMessage(context.Background(), "token", 1, "hello"); err != nil {
		t.Fatal(err)
	}

	got = snapshot()
	if len(got) != 3 || got[2] == got[0] {
		t.Fatal("a new message reused a previous message ID")
	}
}

func TestSendMessageConflictIsNotRetried(t *testing.T) {
	var calls atomic.Int64

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)

		var message map[string]string
		if err := json.NewDecoder(r.Body).Decode(&message); err != nil {
			t.Error(err)
		}

		if message["message_id"] != "caller-selected-id" {
			t.Error("caller-provided message ID was changed")
		}

		http.Error(w, `{"error":"conflicting message ID"}`, http.StatusConflict)
	}))
	defer server.Close()

	err := NewClient(
		server.URL,
	).SendMessage(context.Background(), "token", 1, "changed", "caller-selected-id")

	var failure *Error
	if !errors.As(err, &failure) || failure.StatusCode != http.StatusConflict || calls.Load() != 1 {
		t.Fatalf("conflict was not returned directly: calls=%d, err=%v", calls.Load(), err)
	}
}
