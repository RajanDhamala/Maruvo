package providers

import (
	"context"
	"os/exec"
	"strconv"
	"time"
)

func shellCommand(ctx context.Context, text string) *exec.Cmd {
	command := exec.CommandContext(ctx, "cmd.exe", "/C", text)
	command.Cancel = func() error {
		cleanup, stop := context.WithTimeout(context.Background(), 5*time.Second)
		defer stop()
		return exec.CommandContext(cleanup, "taskkill", "/T", "/F", "/PID", strconv.Itoa(command.Process.Pid)).Run()
	}
	return command
}
