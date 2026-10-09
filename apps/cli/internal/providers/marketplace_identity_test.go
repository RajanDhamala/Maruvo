package providers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/rajandhamala/Maruvo/cli/internal/api"
)

func TestAcceptedTasksIdentifyBothAccountsWithoutConflatingFunding(t *testing.T) {
	workerID := int64(1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/posts/urs" {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		json.NewEncoder(w).Encode(map[string]any{"posts": []api.Post{
			{ID: 4, UserID: 3, AcceptedBy: &workerID, Status: "negotiating", Remote: api.RemoteStatus{Status: "waiting_for_funding"}},
			{ID: 2, UserID: 3, Status: "open"},
		}})
	}))
	defer server.Close()
	for _, test := range []struct{ user, profile, role string }{{"3", "poster", "requester"}, {"1", "default", "worker"}} {
		m := &Marketplace{client: api.NewClient(server.URL), token: "session", profile: test.profile}
		m.SetAccount(api.User{ID: test.user})
		output, err := m.execute(t.Context(), marketCall("my_posts", `{}`))
		if err != nil {
			t.Fatal(err)
		}
		var result struct {
			Account map[string]string `json:"current_account"`
			Posts   []struct {
				ID         int64  `json:"id"`
				UserID     int64  `json:"user_id"`
				AcceptedBy *int64 `json:"accepted_by"`
				Accepted   bool   `json:"accepted"`
				Role       string `json:"your_role"`
				Remote     string `json:"remote_status"`
			} `json:"posts"`
		}
		if err := json.Unmarshal([]byte(output), &result); err != nil {
			t.Fatal(err)
		}
		if result.Account["user_id"] != test.user || result.Account["profile"] != test.profile || len(result.Posts) != 2 {
			t.Fatalf("wrong account/list: %s", output)
		}
		post := result.Posts[0]
		if post.ID != 4 || post.UserID != 3 || post.AcceptedBy == nil || *post.AcceptedBy != 1 || !post.Accepted || post.Role != test.role || post.Remote != "waiting_for_funding" {
			t.Fatalf("wrong accepted task semantics for %s: %s", test.profile, output)
		}
		if result.Posts[1].Accepted || result.Posts[1].AcceptedBy != nil {
			t.Fatal("open task marked accepted")
		}
	}
	scoped := &Marketplace{scoped: true}
	scoped.SetAccount(api.User{ID: "3", Email: "owner@example.com"})
	if scoped.account.ID != "" {
		t.Fatal("scoped credential inherited owner identity")
	}
}

func TestWorkspaceAgentToolsRemainBoundToAuthorizedTask(t *testing.T) {
	m := &Marketplace{TaskID: 4, FundingUI: true}
	if m.handles("find_posts") || m.handles("accept_post") || m.handles("open_funding") || m.handles("wait_remote") {
		t.Fatal("workspace loop exposed discovery, navigation or polling")
	}
	if !m.handles("get_workspace") || !m.handles("submit_delivery") {
		t.Fatal("workspace loop lost collaboration tools")
	}
	if _, err := m.execute(t.Context(), marketCall("send_message", `{"post":5,"text":"outside task","message_id":"x"}`)); err == nil {
		t.Fatal("cross-task mutation allowed")
	}
}
