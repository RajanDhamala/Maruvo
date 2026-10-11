package work

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/rajandhamala/Maruvo/cli/internal/agent"
	"github.com/rajandhamala/Maruvo/cli/internal/api"
)

func TestWorkRequestsRejectNullAndBindScopedShortcuts(t *testing.T) {
	requests := make(chan map[string]any, 2)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer mru_work_fixture" {
			t.Error("shortcut request lost its scoped credential")
		}
		var received map[string]any
		if err := json.NewDecoder(r.Body).Decode(&received); err != nil {
			t.Error(err)
		}
		requests <- received
		_ = json.NewEncoder(w).Encode(api.WorkReceipt{ProjectID: "project", Result: mustJSON(map[string]any{})})
	}))
	defer server.Close()
	directory := t.TempDir()
	credential := filepath.Join(directory, "credential.json")
	data, _ := json.Marshal(map[string]string{"api_url": server.URL, "token": "mru_work_fixture"})
	if err := os.WriteFile(credential, data, 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("MARUVO_AGENT_TOKEN_FILE", credential)
	requestFile := filepath.Join(directory, "request.json")
	if err := os.WriteFile(requestFile, []byte("null"), 0600); err != nil {
		t.Fatal(err)
	}
	var output, log bytes.Buffer
	err := Run(context.Background(), api.NewClient(server.URL), "default", []string{"submit", "--project", "project", "--file", requestFile}, &output, &log)
	if err == nil {
		t.Fatal("null request was accepted")
	}
	if len(requests) != 0 {
		t.Fatal("invalid request reached the API")
	}
	var errorOutput bytes.Buffer
	if err := agent.WriteError(&errorOutput, err); err != nil {
		t.Fatal(err)
	}
	var structured struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(errorOutput.Bytes(), &structured); err != nil || structured.Error.Code != "INVALID_ARGUMENT" {
		t.Fatalf("null request did not produce a structured argument error: %s", errorOutput.String())
	}
	if err := os.WriteFile(requestFile, []byte(`{"operation_id":"block-1","next_action":"install compiler"}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := Run(context.Background(), api.NewClient(server.URL), "default", []string{"block", "--project", "project", "--assignment", "patch", "--attempt", "attempt", "--text", "compiler missing", "--file", requestFile}, &output, &log); err != nil {
		t.Fatal(err)
	}
	received := <-requests
	if received["reason"] != "compiler missing" || received["assignment_id"] != "patch" || received["attempt_id"] != "attempt" || received["next_action"] != "install compiler" || received["note"] != nil {
		t.Fatalf("block shortcuts changed request meaning: %#v", received)
	}
	if err := Run(context.Background(), api.NewClient(server.URL), "default", []string{"revoke", "--project", "project", "--grant-id", "grant", "--operation-id", "revoke-1"}, &output, &log); err != nil {
		t.Fatal(err)
	}
	received = <-requests
	if received["grant_id"] != "grant" || received["contribution_id"] != nil {
		t.Fatalf("revocation shortcut targeted the wrong ID: %#v", received)
	}
	if err := Run(context.Background(), api.NewClient(server.URL), "default", []string{"assign", "--project", "project", "--assignment", "patch", "--user", "42", "--operation-id", "assign-1"}, &output, &log); err != nil {
		t.Fatal(err)
	}
	received = <-requests
	if received["user_id"] != float64(42) || received["assignment_id"] != "patch" || received["action"] != "assign" {
		t.Fatalf("assignment shortcut changed recipient or task: %#v", received)
	}
}
