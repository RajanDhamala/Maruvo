package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rajandhamala/Maruvo/cli/internal/api"
	"github.com/rajandhamala/Maruvo/cli/internal/auth"
)

func TestAgentCredentialCommands(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("MARUVO_AGENT_TOKEN_FILE", "")

	_ = os.Unsetenv("MARUVO_AGENT_TOKEN_FILE")
	creates := 0

	var (
		scope      map[string]any
		usedTokens []string
	)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		usedTokens = append(usedTokens, r.Header.Get("Authorization"))
		switch r.URL.Path {
		case "/me":
			_, _ = w.Write([]byte(`{"id":"2","username":"owner"}`))
		case "/posts/7/agents":
			if r.Method == http.MethodGet {
				_, _ = w.Write([]byte(`[{"id":"grant","post_id":7}]`))
				return
			}

			creates++
			_ = json.NewDecoder(r.Body).Decode(&scope)
			_, _ = w.Write(
				[]byte(`{"token":"mru_agent_private","grant":{"id":"grant","post_id":7,"name":"codex"}}`),
			)
		case "/agents/grant/revoke":
			_, _ = w.Write([]byte(`{"id":"grant","post_id":7,"revoked_at":"2026-10-04T00:00:00Z"}`))
		default:
			t.Errorf("unexpected route: %s", r.URL.Path)
		}
	}))
	defer server.Close()

	if err := auth.SaveSession(server.URL, "owner-secret", "owner"); err != nil {
		t.Fatal(err)
	}

	path := filepath.Join(t.TempDir(), "codex.json")
	run := func(args ...string) (string, error) {
		t.Helper()

		var out, log bytes.Buffer

		err := Run(context.Background(), api.NewClient(server.URL), "owner", args, &out, &log)
		if strings.Contains(out.String()+log.String(), "mru_agent_private") {
			t.Fatal("credential leaked into command output")
		}

		return out.String(), err
	}

	output, err := run(
		"grant",
		"--post",
		"7",
		"--name",
		"codex",
		"--permission",
		"submit",
		"--expires-in",
		"2h",
		"--to",
		path,
	)
	if err != nil || creates != 1 || !strings.Contains(output, "credential_path") {
		t.Fatalf("grant failed: %s %v", output, err)
	}

	if scope["expires_in_seconds"] != float64(7200) || scope["name"] != "codex" {
		t.Fatalf("wrong grant scope: %v", scope)
	}

	permissions := scope["permissions"].([]any)
	if len(permissions) != 2 || permissions[0] != "read" || permissions[1] != "submit" {
		t.Fatalf("unexpected permissions: %v", permissions)
	}

	stat, err := os.Stat(path)
	if err != nil || stat.Mode().Perm() != 0600 {
		t.Fatalf("credential file is not private: %v %v", stat, err)
	}

	data, err := os.ReadFile(path)
	if err != nil || !bytes.Contains(data, []byte("mru_agent_private")) ||
		bytes.Contains(data, []byte("owner-secret")) {
		t.Fatalf("credential file invalid: %v", err)
	}

	if _, err := run("grant", "--post", "7", "--name", "again", "--to", path); err == nil || creates != 1 {
		t.Fatal("grant overwrote an existing credential or issued a second secret")
	}

	if _, err := run("grants", "--post", "7"); err != nil {
		t.Fatal(err)
	}

	if _, err := run("revoke", "--grant-id", "grant"); err != nil {
		t.Fatal(err)
	}

	t.Setenv("MARUVO_AGENT_TOKEN_FILE", path)

	if _, err := run("identity"); err != nil {
		t.Fatal(err)
	}

	if usedTokens[len(usedTokens)-1] != "Bearer mru_agent_private" {
		t.Fatal("agent file did not override the account profile")
	}

	t.Setenv("MARUVO_AGENT_TOKEN_FILE", filepath.Join(t.TempDir(), "missing"))

	before := len(usedTokens)
	if _, err := run("identity"); err == nil || len(usedTokens) != before {
		t.Fatal("invalid agent file fell back to the account session")
	}
}
