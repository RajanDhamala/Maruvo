package agent

import (
	"os"
	"path/filepath"
	"testing"
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
