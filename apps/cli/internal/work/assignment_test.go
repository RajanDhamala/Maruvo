package work

import (
	"bytes"
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
)

func TestAssignmentFetchAndHandoffRetainScopedAccess(t *testing.T) {
	project := api.WorkProject{ID: "project", Name: "Fix bug", Status: "active", Revision: 3, Cursor: 10,
		Assignments: []api.WorkAssignment{{ID: "verify", Assignee: 42, Instructions: "private verification brief", State: "blocked", Blocker: "dependency patch needs an accepted contribution", NextAction: "lead accepts patch", Dependencies: []string{"patch"}, Inputs: []string{"source"}, ExpectedOutputs: []string{"tests.log"}}},
		Artifacts:   []api.WorkArtifact{{ID: "source", SHA256: "exact-source"}, {ID: "patch-file", AssignmentID: "patch", SHA256: "exact-patch"}, {ID: "unrelated", AssignmentID: "other"}},
	}
	var requests atomic.Int32
	var denied atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.Method != "GET" || r.URL.Path != "/work/projects/project" {
			t.Errorf("fetch performed another operation: %s %s", r.Method, r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer mru_work_fixture" {
			t.Error("fetch lost scoped operator credentials")
		}
		if denied.Load() {
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(`{"error":"credential revoked"}`))
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"project": project})
	}))
	defer server.Close()
	credential := filepath.Join(t.TempDir(), "access.json")
	data, _ := json.Marshal(map[string]string{"api_url": server.URL, "token": "mru_work_fixture"})
	if err := os.WriteFile(credential, data, 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("MARUVO_AGENT_TOKEN_FILE", credential)
	client := api.NewClient(server.URL)
	var output bytes.Buffer
	if err := Run(t.Context(), client, "sender-profile", []string{"assignment", "--project", "project", "--assignment", "verify"}, &output, &output); err != nil {
		t.Fatal(err)
	}
	var details AssignmentDetails
	if err := json.Unmarshal(output.Bytes(), &details); err != nil {
		t.Fatal(err)
	}
	if details.ProjectID != "project" || details.Revision != 3 || details.Cursor != 10 || details.Assignment.Assignee != 42 || details.Assignment.Blocker == "" || details.Assignment.NextAction == "" || len(details.AvailableArtifacts) != 2 || details.AvailableArtifacts[1].SHA256 != "exact-patch" {
		t.Fatalf("fetch lost assignment or artifact context: %#v", details)
	}
	output.Reset()
	if err := Run(t.Context(), client, "sender-profile", []string{"handoff", "--project", "project", "--assignment", "verify"}, &output, &output); err != nil {
		t.Fatal(err)
	}
	var handoff struct {
		Prompt string `json:"prompt"`
	}
	if err := json.Unmarshal(output.Bytes(), &handoff); err != nil {
		t.Fatal(err)
	}
	for _, text := range []string{server.URL, "work assignment --project 'project' --assignment 'verify'", "YOUR_PROFILE", "user 42", "confirmed-funding", "90-second lease"} {
		if !strings.Contains(handoff.Prompt, text) {
			t.Fatalf("handoff omitted %q", text)
		}
	}
	for _, secret := range []string{"mru_work_fixture", "sender-profile", "private verification brief", "exact-source", "unrelated"} {
		if strings.Contains(handoff.Prompt, secret) {
			t.Fatalf("handoff exposed %q", secret)
		}
	}
	output.Reset()
	err := Run(t.Context(), client, "sender-profile", []string{"assignment", "--project", "project", "--assignment", "hidden"}, &output, &output)
	var apiErr *api.Error
	if !errors.As(err, &apiErr) || apiErr.StatusCode != 404 || output.Len() != 0 {
		t.Fatalf("hidden assignment did not fail closed: %v", err)
	}
	before := requests.Load()
	for _, args := range [][]string{{"assignment", "--project", "project"}, {"handoff", "--project", "project"}, {"assign", "--project", "project", "--user", "0"}, {"assignment", "--project", "project", "--assignment", "verify", "--user", "42"}} {
		if err := Run(t.Context(), client, "default", args, &output, &output); err == nil {
			t.Fatalf("invalid arguments were accepted: %v", args)
		}
	}
	if requests.Load() != before {
		t.Fatal("invalid fetch arguments reached the API")
	}
	denied.Store(true)
	err = Run(t.Context(), client, "default", []string{"assignment", "--project", "project", "--assignment", "verify"}, &output, &output)
	if !errors.As(err, &apiErr) || apiErr.StatusCode != 403 || output.Len() != 0 || requests.Load() != before+1 {
		t.Fatalf("revoked credential did not fail without fallback: %v", err)
	}
}

func TestPaidChildHandoffUsesItsTaskWorkflow(t *testing.T) {
	prompt := HandoffPrompt("https://example.com", "project", api.WorkAssignment{ID: "verify", Assignee: 42, ChildPost: 17})
	if !strings.Contains(prompt, "agent task --post 17") || strings.Contains(prompt, "claim with a fresh operation ID") || !strings.Contains(prompt, "confirmed funding") {
		t.Fatal("paid child handoff chose the project execution path")
	}
}
