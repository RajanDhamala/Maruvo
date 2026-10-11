package providers

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestAgentToolEventsDescribeTheActualFilesAndResults(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\n"), 0600); err != nil {
		t.Fatal(err)
	}

	turn := 0
	client := mockClient(t, "deepseek", func(w http.ResponseWriter, r *http.Request) {
		turn++
		if turn == 1 {
			calls := []ToolCall{
				{
					ID:       "list",
					Type:     "function",
					Function: Function{Name: "list_files", Arguments: `{"path":"."}`},
				},
				{
					ID:       "read",
					Type:     "function",
					Function: Function{Name: "read_file", Arguments: `{"path":"main.go"}`},
				},
				{
					ID:       "missing",
					Type:     "function",
					Function: Function{Name: "read_file", Arguments: `{"path":"missing.go"}`},
				},
			}
			json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": Message{
				Role: "assistant", ToolCalls: calls,
			}}}})

			return
		}

		data, _ := io.ReadAll(r.Body)
		if !strings.Contains(string(data), "package main") || !strings.Contains(string(data), "Tool error:") {
			t.Error("actual tool results did not reach the model")
		}

		io.WriteString(w, `{"choices":[{"message":{"role":"assistant","content":"Read the file."}}]}`)
	})

	var events []Event

	_, err := client.RunAgent(t.Context(), dir, nil, "Read the project", nil, func(event Event) {
		if event.Type == "tool" {
			events = append(events, event)
		}
	})
	if err != nil || len(events) != 6 {
		t.Fatalf("tool lifecycle: %v %+v", err, events)
	}

	for i, id := range []string{"list", "read", "missing"} {
		start, end := events[2*i], events[2*i+1]
		if start.ToolID != id || end.ToolID != id || start.Status != "running" {
			t.Fatalf("tool events cannot be paired: %+v", events)
		}
	}

	if !strings.Contains(events[1].Text, "1 entry") || !strings.Contains(events[3].Text, "main.go") ||
		!strings.Contains(events[3].Text, "1 line · 13 bytes") || events[5].Status != "failed" ||
		!strings.Contains(events[5].Text, "missing.go") || strings.Contains(events[3].Text, "package main") ||
		events[3].Preview != "package main" || events[2].Preview != "" || events[5].Preview != "" {
		t.Fatalf("file details missing or file content leaked: %+v", events)
	}
}

func TestAgentToolPreviewsUseActualWritesAndCommandOutput(t *testing.T) {
	dir, turn := t.TempDir(), 0
	client := mockClient(t, "deepseek", func(w http.ResponseWriter, r *http.Request) {
		turn++
		message := Message{Role: "assistant", Content: "Finished."}
		if turn == 1 {
			message.Content = ""
			message.ToolCalls = []ToolCall{
				{ID: "write", Type: "function", Function: Function{Name: "write_file", Arguments: `{"path":"hello.asm","content":"section .data\nmessage db 'Hello', 10\nsection .text\n"}`}},
				{ID: "run", Type: "function", Function: Function{Name: "run_command", Arguments: `{"command":"printf 'Hello\\n'; exit 7"}`}},
			}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": message}}})
	})
	var events []Event
	_, err := client.RunAgent(t.Context(), dir, nil, "Write and run", func(context.Context, Approval) (bool, error) { return true, nil },
		func(event Event) {
			if event.Type == "tool" {
				events = append(events, event)
			}
		}, &Marketplace{Commands: true})
	if err != nil || len(events) != 4 {
		t.Fatalf("tool lifecycle: %v %+v", err, events)
	}
	content, err := os.ReadFile(filepath.Join(dir, "hello.asm"))
	if err != nil || events[1].Preview != strings.TrimSuffix(string(content), "\n") ||
		events[3].Preview != "Hello" || events[3].Status != "failed" || !strings.Contains(events[3].Text, "Exit 7") {
		t.Fatalf("preview does not match the actual file and command result: %+v", events)
	}
}

func TestAgentPreviewRedactsBeforeTruncation(t *testing.T) {
	dir, turn := t.TempDir(), 0
	var key string
	client := mockClient(t, "deepseek", func(w http.ResponseWriter, r *http.Request) {
		turn++
		message := Message{Role: "assistant", Content: "Finished."}
		if turn == 1 {
			message.Content = ""
			arguments, _ := json.Marshal(map[string]string{"path": strings.Repeat("x", 235) + key})
			message.ToolCalls = []ToolCall{
				{ID: "read", Type: "function", Function: Function{Name: "read_file", Arguments: `{"path":"source.txt"}`}},
				{ID: "long-path", Type: "function", Function: Function{Name: "read_file", Arguments: string(arguments)}},
			}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": message}}})
	})
	key = client.key
	content := strings.Repeat("x", 125) + client.key + "\nlogin-secret"
	if err := os.WriteFile(filepath.Join(dir, "source.txt"), []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	var preview, heading string
	_, err := client.RunAgent(t.Context(), dir, nil, "Read", nil, func(event Event) {
		if event.ToolID == "read" && event.Status == "succeeded" {
			preview = event.Preview
		}
		if event.ToolID == "long-path" {
			heading += event.Text
		}
	}, &Marketplace{token: "login-secret"})
	if err != nil || preview == "" || strings.Contains(preview, client.key) || strings.Contains(preview, "login-secret") ||
		!strings.Contains(preview, "[login token redacted]") || strings.Contains(preview, "x"+client.key[:min(6, len(client.key))]) ||
		strings.Contains(heading, "x"+client.key[:min(2, len(client.key))]) {
		t.Fatalf("preview leaked a known credential at its truncation boundary: %v %q", err, preview)
	}
}

func TestToolPreviewIsBoundedAndStripsTerminalControls(t *testing.T) {
	preview := shortToolPreview("\x1b[31m" + strings.Repeat("界", 200) + "\x00\n" + strings.Repeat("line\n", 20))
	lines := strings.Split(preview, "\n")
	if !utf8.ValidString(preview) || len(lines) != 6 || utf8.RuneCountInString(lines[0]) != 140 ||
		strings.Contains(preview, "\x1b") || strings.Contains(preview, "\x00") || lines[5] != "… preview truncated" {
		t.Fatal("tool preview exceeded its line limit or retained terminal controls")
	}
}
