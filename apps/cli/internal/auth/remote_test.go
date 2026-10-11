package auth

import (
	"os"
	"reflect"
	"testing"
)

func TestRemoteStatePrivateAndListenerExclusive(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	value := HarnessConnection{
		APIURL:     "http://localhost:3000",
		Account:    "1",
		Executable: "/bin/true",
		Directory:  "/tmp/work",
	}
	if err := SaveRemoteState("test", "harness.json", value); err != nil {
		t.Fatal(err)
	}

	var loaded HarnessConnection
	if err := LoadRemoteState(
		"test",
		"harness.json",
		&loaded,
	); err != nil ||
		!reflect.DeepEqual(loaded, value) {
		t.Fatalf("load: %+v %v", loaded, err)
	}

	unlock, err := LockRemoteInbox("test")
	if err != nil {
		t.Fatal(err)
	}

	if extra, err := LockRemoteInbox("test"); err == nil {
		extra()
		t.Fatal("second listener acquired lock")
	}

	unlock()

	unlock, err = LockRemoteInbox("test")
	if err != nil {
		t.Fatal("lock did not recover after exit", err)
	}

	unlock()

	path, _ := RemotePath("test", "harness.json")
	if err := os.Chmod(path, 0644); err != nil {
		t.Fatal(err)
	}

	if LoadRemoteState("test", "harness.json", &loaded) == nil {
		t.Fatal("read public harness config")
	}

	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}

	if err := os.Symlink("/etc/passwd", path); err != nil {
		t.Fatal(err)
	}

	if SaveRemoteState("test", "harness.json", value) == nil {
		t.Fatal("saved through a symlink")
	}
}
