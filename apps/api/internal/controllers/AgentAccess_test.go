package controller

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/rajandhamala/Maruvo/internal/utils"
)

func TestAgentRouteBoundaries(t *testing.T) {
	grant := utils.AgentAccess{
		PostID:      7,
		Permissions: []string{"read", "message", "upload", "submit", "request-changes"},
	}

	for _, test := range []struct {
		pattern string
		path    string
		id      string
		allowed bool
	}{
		{"GET /posts/{id}/workspace", "/posts/7/workspace", "7", true},
		{"POST /posts/{id}/messages", "/posts/7/messages", "7", true},
		{"POST /posts/{id}/activity", "/posts/7/activity", "7", true},
		{"POST /posts/{id}/activity", "/posts/8/activity", "8", false},
		{"GET /agent/inbox", "/agent/inbox", "", false},
		{"POST /posts/{id}/submit", "/posts/7/submit", "7", true},
		{"POST /posts/{id}/review/changes", "/posts/7/review/changes", "7", true},
		{"GET /posts/{id}/workspace", "/posts/8/workspace", "8", false},
		{"GET /posts/{id}/workspace", "/posts/0/workspace", "0", false},
		{"GET /ws", "/ws?post_id=7", "", true},
		{"GET /ws", "/ws?post_id=8", "", false},
		{"GET /ws/files", "/ws/files?post_id=7", "", true},
		{"GET /ws/files", "/ws/files?post_id=8", "", false},
		{"POST /posts/create", "/posts/create", "", false},
		{"POST /posts/feed", "/posts/feed", "", false},
		{"POST /posts/accept", "/posts/accept", "", false},
		{"POST /posts/{id}/agents", "/posts/7/agents", "7", false},
		{"GET /posts/{id}/agents", "/posts/7/agents", "7", false},
		{"GET /posts/{id}/agent-control", "/posts/7/agent-control", "7", false},
		{"PUT /posts/{id}/agent-control", "/posts/7/agent-control", "7", false},
		{"POST /agents/{grant}/revoke", "/agents/anything/revoke", "", false},
		{"POST /oauth/github/link", "/oauth/github/link", "", false},
		{"GET /wallet", "/wallet", "", false},
		{"POST /posts/fund", "/posts/fund", "", false},
		{"POST /posts/{id}/settle", "/posts/7/settle", "7", false},
		{"POST /posts/{id}/settle/submit", "/posts/7/settle/submit", "7", false},
		{"POST /future/route", "/future/route", "", false},
	} {
		r := httptest.NewRequest(http.MethodGet, test.path, nil)
		r.Pattern = test.pattern
		r.SetPathValue("id", test.id)

		if allowed := agentRequestAllowed(grant, r); allowed != test.allowed {
			t.Errorf("%s %s: allowed=%v", test.pattern, test.path, allowed)
		}
	}

	grant.Permissions = []string{"read"}
	r := httptest.NewRequest(http.MethodPost, "/posts/7/messages", nil)
	r.Pattern = "POST /posts/{id}/messages"
	r.SetPathValue("id", "7")

	if agentRequestAllowed(grant, r) {
		t.Fatal("read permission allowed a mutation")
	}
}

func TestAgentEventAttribution(t *testing.T) {
	access := &utils.AgentAccess{ID: "grant", Name: "codex", OwnerID: 2, PostID: 7, ExpiresAt: time.Now()}
	ctx := context.WithValue(context.Background(), utils.AgentKey, access)

	payload, err := agentData(ctx, map[string]any{"text": "ready", "agent_grant_id": "forged"})
	if err != nil {
		t.Fatal(err)
	}

	var fields map[string]any
	if err := json.Unmarshal(payload, &fields); err != nil {
		t.Fatal(err)
	}

	if fields["agent_grant_id"] != "grant" || fields["agent_name"] != "codex" || fields["text"] != "ready" {
		t.Fatalf("actor attribution lost: %s", payload)
	}

	payload, err = agentData(context.Background(), map[string]string{"text": "human"})
	if err != nil || string(payload) != `{"text":"human"}` {
		t.Fatalf("human payload changed: %s %v", payload, err)
	}
}
