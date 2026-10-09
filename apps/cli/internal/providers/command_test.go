package providers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestCommandNeedsApprovalAndShowsExactCommand(t *testing.T) {
	directory := t.TempDir()
	call := marketCall("run_command", `{"command":"printf approved > result.txt"}`)
	if _, err := executeCommand(t.Context(), directory, call, nil); err == nil {
		t.Fatal("ran without local approval")
	}
	if _, err := executeCommand(t.Context(), directory, call, func(context.Context, Approval) (bool, error) { return false, nil }); err == nil {
		t.Fatal("ran declined command")
	}
	if _, err := os.Stat(filepath.Join(directory, "result.txt")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("declined command mutated disk")
	}
	result, err := executeCommand(t.Context(), directory, call, func(_ context.Context, a Approval) (bool, error) {
		if a.Action != "Run command" || a.Path != directory || a.Content != "printf approved > result.txt" {
			t.Fatalf("wrong approval %+v", a)
		}
		return true, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	var output struct {
		ExitCode int `json:"exit_code"`
	}
	if json.Unmarshal([]byte(result), &output) != nil || output.ExitCode != 0 {
		t.Fatal(result)
	}
	data, _ := os.ReadFile(filepath.Join(directory, "result.txt"))
	if string(data) != "approved" {
		t.Fatal("approved command did not run")
	}
}

func TestCommandEnvironmentOutputAndExitCode(t *testing.T) {
	t.Setenv("MARUVO_TOKEN", "never-pass-this")
	result, err := executeCommand(t.Context(), t.TempDir(), marketCall("run_command", `{"command":"test -z \"$MARUVO_TOKEN\" || exit 99; printf test-output; exit 7"}`), func(context.Context, Approval) (bool, error) { return true, nil })
	if err != nil {
		t.Fatal(err)
	}
	var output struct {
		Code   int `json:"exit_code"`
		Output string
	}
	if json.Unmarshal([]byte(result), &output) != nil || output.Code != 7 || output.Output != "test-output" {
		t.Fatal(result)
	}
	b := &commandOutput{}
	data := make([]byte, 20000)
	if n, err := b.Write(data); n != len(data) || err != nil || len(b.data) != 16<<10 || !b.truncated {
		t.Fatal("unbounded command output")
	}
}

func TestCommandTimeoutAndHiddenControlRejection(t *testing.T) {
	approve := func(context.Context, Approval) (bool, error) { return true, nil }
	started := time.Now()
	_, err := executeCommand(t.Context(), t.TempDir(), marketCall("run_command", `{"command":"sleep 30","timeout_seconds":1}`), approve)
	if !errors.Is(err, context.DeadlineExceeded) || time.Since(started) > 4*time.Second {
		t.Fatalf("command did not stop promptly: %v", err)
	}
	_, err = executeCommand(t.Context(), t.TempDir(), marketCall("run_command", `{"command":"echo harmless\u001b[2J"}`), approve)
	if err == nil {
		t.Fatal("hidden command controls allowed")
	}
}

func TestModelCannotInvokeCommandWhenToolIsUnavailable(t *testing.T) {
	for _, scoped := range []bool{false, true} {
		t.Run(map[bool]string{false: "headless", true: "task-scoped"}[scoped], func(t *testing.T) {
			step := 0
			client := mockClient(t, "openrouter", func(w http.ResponseWriter, r *http.Request) {
				var request struct {
					Messages []Message `json:"messages"`
					Tools    []Tool    `json:"tools"`
				}
				json.NewDecoder(r.Body).Decode(&request)
				for _, tool := range request.Tools {
					if tool.Function.Name == "run_command" {
						t.Error("command exposed without TUI permission integration")
					}
				}
				message := Message{Role: "assistant"}
				if step == 0 {
					message.ToolCalls = []ToolCall{marketCall("run_command", `{"command":"touch forbidden"}`)}
				} else {
					if !strings.Contains(request.Messages[len(request.Messages)-1].Content, "unavailable") {
						t.Error("unsupported call not rejected")
					}
					message.Content = "Command unavailable."
				}
				step++
				json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": message}}})
			})
			directory := t.TempDir()
			market := &Marketplace{scoped: scoped, Commands: scoped}
			_, err := client.RunAgent(t.Context(), directory, nil, "Test command access", func(context.Context, Approval) (bool, error) {
				t.Error("unsupported command reached approval")
				return true, nil
			}, func(Event) {}, market)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(filepath.Join(directory, "forbidden")); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("unavailable command ran")
			}
		})
	}
}
