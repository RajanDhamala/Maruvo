package agent

import (
	"bytes"
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

func TestConnectBindsHarnessToAccountWithoutSellingRequesterCapacity(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	account := "1"

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/me" {
			t.Errorf("requester connection published an offer: %s", r.URL.Path)
			w.WriteHeader(500)

			return
		}

		_ = json.NewEncoder(w).Encode(api.User{ID: account, Username: "requester"})
	}))
	defer server.Close()

	if err := auth.SaveSession(server.URL, "owner-token", "requester"); err != nil {
		t.Fatal(err)
	}

	var out, log bytes.Buffer

	directory := t.TempDir()
	if err := Run(
		t.Context(),
		api.NewClient(server.URL),
		"requester",
		[]string{"connect", "--exec", "/bin/cat", "--arg=secret-argument", "--prompt", "--dir", directory},
		&out,
		&log,
	); err != nil {
		t.Fatal(err)
	}

	if strings.Contains(out.String(), "secret-argument") || !strings.Contains(out.String(), "agent listen") {
		t.Fatal("connection output exposed arguments or wrong next command")
	}

	saved, err := loadConnection(server.URL, "requester", "1")
	if err != nil || !saved.Prompt || saved.Directory != directory || !filepath.IsAbs(saved.Executable) ||
		len(saved.Arguments) != 1 {
		t.Fatalf("connection lost settings: %+v %v", saved, err)
	}

	if _, err = loadConnection(server.URL, "requester", "2"); err == nil {
		t.Fatal("saved harness used by another account")
	}

	if _, err = loadConnection("http://another-api", "requester", "1"); err == nil {
		t.Fatal("saved harness used against another API")
	}

	file, _ := auth.RemotePath("requester", "harness.json")

	info, err := os.Stat(file)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatal("saved arguments are not private")
	}
}
