package tui

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/rajandhamala/Maruvo/cli/internal/api"
)

func TestBrowserLinkedAccountCanCreateWithoutKeypair(t *testing.T) {
	t.Setenv("MARUVO_WALLET", "browser")
	const address = "23RP5zrXxYYqmSULEuHow6t7P2igrdqm8hgALdmcEQn4"
	var created atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer session" {
			t.Error("missing account session")
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/wallet":
			w.Write([]byte(`{"address":"` + address + `"}`))
		case "/posts/create":
			created.Store(true)
			w.Write([]byte(`{"post":{"id":42}}`))
		default:
			t.Errorf("unexpected request %s", r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	defer server.Close()
	m := chatModel()
	m.ctx, m.token, m.client = context.Background(), "session", api.NewClient(server.URL)
	result := m.createPost(api.CreatePostPayload{})().(postChanged)
	if result.err != nil || result.wallet != address || !created.Load() {
		t.Fatalf("browser creation: %#v", result)
	}
}
