package providers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/rajandhamala/Maruvo/cli/internal/api"
)

func TestFundingToolOnlyRequestsEligibleHumanScreen(t *testing.T) {
	worker := int64(1)
	info := api.PostInfo{Post: api.Post{ID: 4, UserID: 3, AcceptedBy: &worker, Status: "negotiating"}, Escrow: api.Escrow{State: "unfunded"}}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/posts/info" {
			t.Errorf("unexpected funding mutation %s", r.URL.Path)
		}
		json.NewEncoder(w).Encode(info)
	}))
	defer server.Close()
	m := &Marketplace{client: api.NewClient(server.URL), token: "session", FundingUI: true}
	m.SetAccount(api.User{ID: "3"})
	result, err := m.execute(t.Context(), marketCall("open_funding", `{"post":4}`))
	if err != nil {
		t.Fatal(err)
	}
	var output struct {
		Post   int64
		Signed bool
		Status string
	}
	if json.Unmarshal([]byte(result), &output) != nil || output.Post != 4 || output.Signed || output.Status != "funding_screen_requested" {
		t.Fatalf("unexpected result %s", result)
	}
	for _, state := range []string{"pending", "confirmed", "released", "refunded"} {
		info.Escrow.State = state
		if _, err := m.execute(t.Context(), marketCall("open_funding", `{"post":4}`)); err == nil {
			t.Fatalf("allowed state %s", state)
		}
	}
	info.Escrow.State = "unfunded"
	m.SetAccount(api.User{ID: "1"})
	if _, err := m.execute(t.Context(), marketCall("open_funding", `{"post":4}`)); err == nil {
		t.Fatal("worker opened funding")
	}
	m.FundingUI = false
	if m.handles("open_funding") {
		t.Fatal("headless tool exposed")
	}
	m.FundingUI, m.scoped = true, true
	if m.handles("open_funding") {
		t.Fatal("scoped tool exposed")
	}
}

func TestWorkspaceToolChecksAccessAndReturnsCompactHandoff(t *testing.T) {
	worker := int64(1)
	info := api.PostInfo{Post: api.Post{ID: 4, UserID: 3, AcceptedBy: &worker, Status: "in_progress"}, Escrow: api.Escrow{State: "confirmed"}}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/posts/info":
			json.NewEncoder(w).Encode(info)
		case "/posts/4/workspace":
			json.NewEncoder(w).Encode(api.Workspace{Post: info.Post, Escrow: info.Escrow})
		default:
			t.Errorf("unexpected action %s", r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	defer server.Close()
	m := &Marketplace{client: api.NewClient(server.URL), token: "session", FundingUI: true}
	for _, user := range []string{"3", "1"} {
		m.SetAccount(api.User{ID: user})
		result, err := m.execute(t.Context(), marketCall("open_workspace", `{"post":4}`))
		if err != nil {
			t.Fatal(err)
		}
		var output struct {
			Post            int64
			Status, Funding string
		}
		if json.Unmarshal([]byte(result), &output) != nil || output.Post != 4 || output.Status != "workspace_screen_requested" || output.Funding != "confirmed" {
			t.Fatalf("bad handoff %s", result)
		}
	}
	info.Post.AcceptedBy = nil
	if _, err := m.execute(t.Context(), marketCall("open_workspace", `{"post":4}`)); err == nil {
		t.Fatal("unaccepted workspace opened")
	}
	info.Post.AcceptedBy = &worker
	m.SetAccount(api.User{ID: "8"})
	if _, err := m.execute(t.Context(), marketCall("open_workspace", `{"post":4}`)); err == nil {
		t.Fatal("nonparticipant workspace opened")
	}
	m.FundingUI = false
	if m.handles("open_workspace") {
		t.Fatal("headless workspace navigation exposed")
	}
	m.FundingUI, m.scoped = true, true
	if m.handles("open_workspace") {
		t.Fatal("scoped workspace navigation exposed")
	}
}
