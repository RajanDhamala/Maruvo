package auth

import (
	"os"
	"path/filepath"
	"testing"
)

func TestAgentCredentialURLBinding(t *testing.T) {
	path := filepath.Join(t.TempDir(), "agent.json")
	if err := os.WriteFile(
		path,
		[]byte(`{"api_url":"http://localhost:3000","token":"mru_agent_secret"}`),
		0600,
	); err != nil {
		t.Fatal(err)
	}

	t.Setenv("MARUVO_AGENT_TOKEN_FILE", path)

	if token, set, err := LoadAgentSession(
		"http://localhost:3000",
	); err != nil || !set ||
		token != "mru_agent_secret" {
		t.Fatal("valid agent file was rejected")
	}

	if token, set, err := LoadAgentSession("https://another.example"); err == nil || !set || token != "" {
		t.Fatal("agent token could be sent to another API")
	}

	if err := os.WriteFile(
		path,
		[]byte(`{"api_url":"http://localhost:3000","token":"account-jwt"}`),
		0600,
	); err != nil {
		t.Fatal(err)
	}

	if token, _, err := LoadAgentSession("http://localhost:3000"); err == nil || token != "" {
		t.Fatal("account credential accepted as an agent file")
	}
}
