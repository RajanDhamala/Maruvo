package providers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/rajandhamala/Maruvo/cli/internal/api"
)

func TestBuiltInRemoteActionsUseRevocableTaskCredentials(t *testing.T) {
	for _, test := range []struct {
		name        string
		paused      bool
		revoked     bool
		wantActions int
		wantCleanup int
	}{
		{name: "allowed", wantActions: 1, wantCleanup: 1},
		{name: "manual before grant", paused: true},
		{name: "takeover after grant", revoked: true, wantCleanup: 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			actions, cleanup := 0, 0

			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/posts/7/agents":
					if r.Header.Get("Authorization") != "Bearer owner-secret" {
						t.Error("grant creation did not use human login")
					}

					var payload struct {
						Permissions []string `json:"permissions"`
						Lifetime    int64    `json:"expires_in_seconds"`
					}
					if json.NewDecoder(r.Body).Decode(&payload) != nil ||
						strings.Join(payload.Permissions, ",") != "read,message" || payload.Lifetime != 300 {
						t.Error("built-in action received excessive access")
					}

					if test.paused {
						w.WriteHeader(409)
						_ = json.NewEncoder(w).
							Encode(map[string]string{"error": "agent access is paused for this task"})

						return
					}

					_ = json.NewEncoder(w).Encode(api.AgentCredential{
						Token: "mru_agent_temporary-secret",
						Grant: api.AgentGrant{ID: "temporary", PostID: 7},
					})
				case "/posts/7/messages":
					if r.Header.Get("Authorization") != "Bearer mru_agent_temporary-secret" {
						t.Error("model action bypassed scoped credentials")
					}

					if test.revoked {
						w.WriteHeader(401)
						_ = json.NewEncoder(w).
							Encode(map[string]string{"error": "agent credential mru_agent_temporary-secret revoked"})

						return
					}

					actions++
					_ = json.NewEncoder(w).
						Encode(api.MessageReceipt{MessageID: "stable-message", StreamID: "1-0"})
				case "/agents/temporary/revoke":
					if r.Header.Get("Authorization") != "Bearer owner-secret" {
						t.Error("cleanup lost human login")
					}

					cleanup++
					_ = json.NewEncoder(w).Encode(api.AgentGrant{ID: "temporary"})
				default:
					t.Errorf("unexpected operation: %s", r.URL.Path)
					w.WriteHeader(404)
				}
			}))
			defer server.Close()

			market := &Marketplace{client: api.NewClient(server.URL), token: "owner-secret"}

			result, err := market.execute(t.Context(), marketCall("send_message",
				`{"post":7,"text":"Agent reply","message_id":"stable-message"}`))
			if (err != nil) != (test.paused || test.revoked) || actions != test.wantActions ||
				cleanup != test.wantCleanup {
				t.Fatalf(
					"control bypass or cleanup failure: actions=%d cleanup=%d err=%v",
					actions,
					cleanup,
					err,
				)
			}

			if strings.Contains(result, "secret") {
				t.Fatal("temporary credential reached model context")
			}

			if err != nil && strings.Contains(err.Error(), "secret") {
				t.Fatal("temporary credential reached the model through an error")
			}
		})
	}
}
