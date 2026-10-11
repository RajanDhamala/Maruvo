//go:build unix

package work

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestManagedCancellationStopsHarnessDescendants(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "descendant-still-running")
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	command := exec.CommandContext(ctx, "/bin/sh", "-c", `/bin/sh -c 'sleep 1; echo still-running > "$1"' child "$1" & wait`, "parent", marker)
	configureHarness(command)
	if err := command.Run(); err == nil {
		t.Fatal("managed process ignored cancellation")
	}
	timer := time.NewTimer(1200 * time.Millisecond)
	defer timer.Stop()
	<-timer.C
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatal("descendant outlived the cancelled managed harness")
	}
}
