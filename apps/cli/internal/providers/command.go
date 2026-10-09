package providers

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"
	"unicode"
)

func commandTool() Tool {
	return marketTool("run_command", "Run an authorized shell command on this device in the selected project folder. Local permission mode applies; no OS sandbox or root elevation. Use for implementation/build/tests. Never access credentials, sign payments or change local permissions. Output is capped at 16 KiB.", `{"type":"object","properties":{"command":{"type":"string","maxLength":8000},"timeout_seconds":{"type":"integer","minimum":1,"maximum":300}},"required":["command"],"additionalProperties":false}`)
}

type commandOutput struct {
	sync.Mutex
	data      []byte
	truncated bool
}

func (b *commandOutput) Write(p []byte) (int, error) {
	b.Lock()
	defer b.Unlock()
	n := len(p)
	remaining := (16 << 10) - len(b.data)
	if len(p) > remaining {
		b.truncated = true
		p = p[:remaining]
	}
	b.data = append(b.data, p...)
	return n, nil
}

func executeCommand(ctx context.Context, directory string, call ToolCall, approve Approve) (string, error) {
	var args struct {
		Command string `json:"command"`
		Timeout int    `json:"timeout_seconds"`
	}
	if err := strictArguments(call.Function.Arguments, &args); err != nil {
		return "", err
	}
	if strings.TrimSpace(args.Command) == "" || len(args.Command) > 8000 || strings.ContainsFunc(args.Command, func(r rune) bool { return unicode.IsControl(r) && r != '\n' && r != '\t' }) {
		return "", errors.New("provide a command up to 8000 bytes")
	}
	if args.Timeout == 0 {
		args.Timeout = 120
	}
	if args.Timeout < 1 || args.Timeout > 300 {
		return "", errors.New("command timeout must be 1-300 seconds")
	}
	if approve == nil {
		return "", errors.New("commands require local permission approval")
	}
	allowed, err := approve(ctx, Approval{Action: "Run command", Path: directory, Content: args.Command})
	if err != nil {
		return "", err
	}
	if !allowed {
		return "", errors.New("user declined command execution")
	}
	runCtx, cancel := context.WithTimeout(ctx, time.Duration(args.Timeout)*time.Second)
	defer cancel()
	command := shellCommand(runCtx, args.Command)
	command.Dir = directory
	command.WaitDelay = 2 * time.Second
	for _, name := range []string{"PATH", "HOME", "LANG", "TERM", "TMPDIR", "GOCACHE", "GOPATH", "CARGO_HOME", "RUSTUP_HOME"} {
		if value, ok := os.LookupEnv(name); ok {
			command.Env = append(command.Env, name+"="+value)
		}
	}
	output := &commandOutput{}
	command.Stdout, command.Stderr = output, output
	err = command.Run()
	if runCtx.Err() != nil {
		return "", runCtx.Err()
	}
	code := 0
	if err != nil {
		var exit *exec.ExitError
		if !errors.As(err, &exit) {
			return "", err
		}
		code = exit.ExitCode()
	}
	data, err := json.Marshal(map[string]any{"exit_code": code, "output": strings.ToValidUTF8(string(output.data), "�"), "truncated": output.truncated})
	return string(data), err
}
