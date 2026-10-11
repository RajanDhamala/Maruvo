//go:build unix

package providers

import (
	"context"
	"os/exec"
	"syscall"
)

func shellCommand(ctx context.Context, text string) *exec.Cmd {
	command := exec.CommandContext(ctx, "/bin/sh", "-c", text)
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	command.Cancel = func() error { return syscall.Kill(-command.Process.Pid, syscall.SIGKILL) }
	return command
}
