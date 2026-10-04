package agent

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/rajandhamala/Maruvo/cli/internal/api"
)

func TestDeclaredOutputCannotEscapeRunDirectory(t *testing.T) {
	directory := t.TempDir()
	if err := os.Mkdir(filepath.Join(directory, "output"), 0700); err != nil {
		t.Fatal(err)
	}

	outside := t.TempDir()

	secret := filepath.Join(outside, "secret")
	if err := os.WriteFile(secret, []byte("private"), 0600); err != nil {
		t.Fatal(err)
	}

	if err := os.Symlink(secret, filepath.Join(directory, "output", "link")); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(
		filepath.Join(directory, "output", "patch.diff"),
		[]byte("patch"),
		0600,
	); err != nil {
		t.Fatal(err)
	}

	root, err := os.OpenRoot(directory)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()

	for _, name := range []string{"../secret", secret, "link", "missing", "bad\nname"} {
		if _, err := readOutput(root, name); err == nil {
			t.Fatalf("unsafe or missing output %q was accepted", name)
		}
	}

	data, err := readOutput(root, "patch.diff")
	if err != nil || string(data) != "patch" {
		t.Fatalf("declared regular output: %q, %v", data, err)
	}

	if err := os.RemoveAll(filepath.Join(directory, "output")); err != nil {
		t.Fatal(err)
	}

	if err := os.Symlink(outside, filepath.Join(directory, "output")); err != nil {
		t.Fatal(err)
	}

	if _, err := readOutput(root, "secret"); err == nil {
		t.Fatal("an output directory symlink escaped the run directory")
	}
}

func TestSharedInputsWithoutCreationDeclarations(t *testing.T) {
	now := time.Now()
	workspace := api.Workspace{Post: api.Post{UserID: 1}, Files: []api.WorkspaceFile{
		{ID: "old", Name: "source.txt", UploadedBy: 1, Purpose: "shared", CreatedAt: now},
		{ID: "worker", Name: "worker.txt", UploadedBy: 2, Purpose: "shared", CreatedAt: now},
		{ID: "latest", Name: "source.txt", UploadedBy: 1, Purpose: "input", CreatedAt: now.Add(time.Second)},
		{ID: "poster-output", Name: "output.txt", UploadedBy: 1, Purpose: "output", CreatedAt: now},
		{ID: "brief", Name: "brief.md", UploadedBy: 1, Purpose: "shared", CreatedAt: now},
	}}

	inputs, ready := taskInputs(workspace)
	if !ready || len(inputs) != 2 || inputs[0].ID != "brief" || inputs[1].ID != "latest" {
		t.Fatal("use the latest requester inputs, ordered by filename, and exclude worker/output files")
	}

	workspace.Post.InputFiles = []string{"missing.txt"}
	if _, ready = taskInputs(workspace); ready {
		t.Fatal("legacy declared inputs must still gate the runner")
	}
}
