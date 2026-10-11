package work

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rajandhamala/Maruvo/cli/internal/api"
)

func TestManagedHarnessSubmitsOnlyDeclaredSafeOutputs(t *testing.T) {
	for _, unsafe := range []bool{false, true} {
		t.Run(fmt.Sprint(unsafe), func(t *testing.T) {
			var mu sync.Mutex
			submitted, uploaded := 0, 0
			attempt := api.WorkAttempt{ID: "attempt-id", OperatorID: 2, State: "running", Inputs: []string{}, Dependencies: map[string]string{}, LeaseUntil: time.Now().Add(time.Minute)}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("Authorization") != "Bearer mru_work_fixture" {
					t.Error("harness runner did not retain scoped identity")
				}
				if r.Method == "GET" {
					_ = json.NewEncoder(w).Encode(map[string]any{"project": api.WorkProject{ID: "project", Assignments: []api.WorkAssignment{{ID: "patch", State: "running", ExpectedOutputs: []string{"patch.diff"}, Attempts: []api.WorkAttempt{attempt}}}}})
					return
				}
				var req map[string]any
				if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
					t.Error(err)
				}
				result := any(map[string]any{})
				mu.Lock()
				switch req["action"] {
				case "claim":
					result = attempt
				case "upload":
					uploaded++
					result = api.WorkArtifact{ID: "patch-artifact"}
				case "submit":
					submitted++
					outputs := req["outputs"].([]any)
					if len(outputs) != 1 || outputs[0] != "patch-artifact" {
						t.Error("runner submitted undeclared output")
					}
				}
				mu.Unlock()
				_ = json.NewEncoder(w).Encode(api.WorkReceipt{ProjectID: "project", Result: mustJSON(result)})
			}))
			defer server.Close()
			dir := t.TempDir()
			executable := filepath.Join(dir, "harness.py")
			source := `#!/usr/bin/env python3
import json, os, sys
from pathlib import Path
ctx=json.load(sys.stdin)
assert ctx['mode']=='work'
assert 'mru_work_' not in json.dumps(ctx)
assert 'MARUVO_WALLET' not in os.environ
cred=json.loads(Path(os.environ['MARUVO_AGENT_TOKEN_FILE']).read_text())
assert cred['token']=='mru_work_fixture'
out=Path(ctx['output_directory'])
(out/'extra.txt').write_text('undeclared output')
(out/'patch.diff').write_text('exact patch')
Path(ctx['evidence_file']).write_text(json.dumps({'note':'checked','evidence':[{'command':'fixture test','exit_code':0,'result':'PASS','environment':'python fixture'}]}))
`
			if unsafe {
				source += fmt.Sprintf("(out/'patch.diff').unlink()\n(out/'patch.diff').symlink_to(%q)\n", filepath.Join(dir, "private.txt"))
				if err := os.WriteFile(filepath.Join(dir, "private.txt"), []byte("do not upload"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			if err := os.WriteFile(executable, []byte(source), 0700); err != nil {
				t.Fatal(err)
			}
			t.Setenv("MARUVO_WALLET", "do-not-forward-wallet")
			var out, log bytes.Buffer
			err := Execute(context.Background(), api.NewClient(server.URL), "mru_work_fixture", "project", "patch", "claim-stable", executable, nil, filepath.Join(dir, "attempts"), time.Minute, &out, &log)
			mu.Lock()
			defer mu.Unlock()
			if unsafe {
				if err == nil || submitted != 0 || uploaded != 0 {
					t.Fatalf("symlink escape was submitted: %v %d %d", err, submitted, uploaded)
				}
			} else if err != nil || submitted != 1 || uploaded != 1 {
				t.Fatalf("execution failed: %v %s", err, log.String())
			}
			entries, _ := os.ReadDir(filepath.Join(dir, "attempts"))
			if len(entries) != 1 {
				t.Fatal("fresh attempt directory not created")
			}
			if _, err := os.Stat(filepath.Join(dir, "attempts", entries[0].Name(), "access.json")); !os.IsNotExist(err) {
				t.Fatal("runtime credential was left on disk")
			}
		})
	}
}
func mustJSON(value any) json.RawMessage { data, _ := json.Marshal(value); return data }
func TestWorkCatalogAndCredentialFailureNeverFallsBack(t *testing.T) {
	var out, log bytes.Buffer
	if err := Run(context.Background(), api.NewClient("http://127.0.0.1:1"), "default", []string{"tools"}, &out, &log); err != nil {
		t.Fatal(err)
	}
	var catalog struct {
		Tools []struct {
			Command string         `json:"command"`
			Output  map[string]any `json:"output_schema"`
		} `json:"tools"`
	}
	if err := json.Unmarshal(out.Bytes(), &catalog); err != nil || len(catalog.Tools) != len(Actions)+7 {
		t.Fatal("incomplete JSON catalog")
	}
	for _, tool := range catalog.Tools {
		if !strings.HasPrefix(tool.Command, "work ") || tool.Output["type"] != "object" {
			t.Fatal("invalid catalog tool")
		}
	}
	t.Setenv("MARUVO_AGENT_TOKEN_FILE", filepath.Join(t.TempDir(), "missing.json"))
	if err := Run(context.Background(), api.NewClient("http://127.0.0.1:1"), "default", []string{"list"}, &out, &log); err == nil || !strings.Contains(err.Error(), "credential") {
		t.Fatal("missing scoped credential did not fail closed")
	}
}
