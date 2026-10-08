package providers

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
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
		!strings.Contains(events[5].Text, "missing.go") || strings.Contains(events[3].Text, "package main") {
		t.Fatalf("file details missing or file content leaked: %+v", events)
	}
}
