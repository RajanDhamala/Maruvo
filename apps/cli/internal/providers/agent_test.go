package providers

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"testing"
)

func fileCall(name, path, content string) ToolCall {
	args, _ := json.Marshal(map[string]string{"path": path, "content": content})

	return ToolCall{
		ID:       "file-call",
		Type:     "function",
		Function: Function{Name: name, Arguments: string(args)},
	}
}

func TestLocalFileToolsRequireApprovalAndStayInProject(t *testing.T) {
	directory := t.TempDir()

	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(directory, "brief.md"), []byte("Original brief"), 0600); err != nil {
		t.Fatal(err)
	}

	if err := os.Symlink(outside, filepath.Join(directory, "escape")); err != nil {
		t.Fatal(err)
	}

	outsideFile := filepath.Join(outside, "keep.md")
	if err := os.WriteFile(outsideFile, []byte("Keep outside file"), 0600); err != nil {
		t.Fatal(err)
	}

	if err := os.Link(outsideFile, filepath.Join(directory, "linked.md")); err != nil {
		t.Fatal(err)
	}

	root, err := os.OpenRoot(directory)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()

	approve := func(context.Context, Approval) (bool, error) { return true, nil }

	for _, name := range []string{"../secret", "/etc/passwd", ".env", ".ssh/key", "providers/config.json", "escape/new.md"} {
		for _, tool := range []string{"read_file", "write_file"} {
			if _, err := executeTool(t.Context(), root, fileCall(tool, name, "bad"), approve); err == nil {
				t.Errorf("%s accepted unsafe path %s", tool, name)
			}
		}
	}

	if _, err := executeTool(
		t.Context(),
		root,
		fileCall("write_file", "brief.md", "Changed"),
		nil,
	); err == nil {
		t.Fatal("file edit without approval succeeded")
	}

	decline := func(context.Context, Approval) (bool, error) { return false, nil }
	if _, err := executeTool(
		t.Context(),
		root,
		fileCall("write_file", "brief.md", "Changed"),
		decline,
	); err == nil {
		t.Fatal("declined edit succeeded")
	}

	data, _ := os.ReadFile(filepath.Join(directory, "brief.md"))
	if string(data) != "Original brief" {
		t.Fatal("declined edit changed the original")
	}

	ctx, cancel := context.WithCancel(t.Context())

	cancelDuringApproval := func(context.Context, Approval) (bool, error) {
		cancel()
		return true, nil
	}
	if _, err := executeTool(
		ctx,
		root,
		fileCall("write_file", "new.md", "Changed"),
		cancelDuringApproval,
	); err == nil {
		t.Fatal("cancelled edit succeeded")
	}

	if _, err := executeTool(
		t.Context(),
		root,
		fileCall("write_file", "docs/result.md", "Approved"),
		approve,
	); err != nil {
		t.Fatal(err)
	}

	got, err := executeTool(t.Context(), root, fileCall("read_file", "docs/result.md", ""), nil)
	if err != nil || got != "Approved" {
		t.Fatalf("approved result: %q %v", got, err)
	}

	if _, err := executeTool(
		t.Context(),
		root,
		fileCall("write_file", "linked.md", "New project content"),
		approve,
	); err != nil {
		t.Fatal(err)
	}

	outsideData, _ := os.ReadFile(outsideFile)
	if string(outsideData) != "Keep outside file" {
		t.Fatal("approved replacement must not mutate a hard-linked file outside the project")
	}

	call := fileCall("read_file", "brief.md", "")

	call.Function.Arguments += ` {"path":".env"}`
	if _, err := executeTool(t.Context(), root, call, nil); err == nil {
		t.Fatal("trailing tool arguments must be rejected")
	}

	if _, err := executeTool(
		t.Context(),
		root,
		fileCall("write_file", "hidden.md", "\x1b[2JHidden"),
		approve,
	); err == nil {
		t.Fatal("terminal control bytes must not be hidden from file approval")
	}
}

func TestAgentReadsRequestsApprovalAndContinues(t *testing.T) {
	directory := t.TempDir()
	if err := os.WriteFile(
		filepath.Join(directory, "brief.md"),
		[]byte("Write a greeting"),
		0600,
	); err != nil {
		t.Fatal(err)
	}

	steps := 0
	client := mockClient(t, "openrouter", func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Messages []Message `json:"messages"`
			Tools    []Tool    `json:"tools"`
		}
		if json.NewDecoder(r.Body).Decode(&request) != nil || len(request.Tools) != 3 {
			t.Error("missing local tool definitions")
		}

		message := Message{Role: "assistant"}

		switch steps {
		case 0:
			message.ToolCalls = []ToolCall{fileCall("read_file", "brief.md", "")}
		case 1:
			last := request.Messages[len(request.Messages)-1]
			if last.Role != "tool" || last.Content != "Write a greeting" || last.ToolCallID != "file-call" {
				t.Errorf("missing read result: %+v", last)
			}

			message.ToolCalls = []ToolCall{fileCall("write_file", "greeting.md", "Hello")}
		case 2:
			if last := request.Messages[len(request.Messages)-1]; last.Content != "Saved greeting.md" {
				t.Errorf("missing write result: %+v", last)
			}

			message.Content = "Created greeting.md"
		default:
			t.Error("agent continued after final response")
		}

		steps++

		json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": message}}})
	})
	approved := false
	approve := func(_ context.Context, change Approval) (bool, error) {
		if change.Path != "greeting.md" || change.Content != "Hello" {
			t.Fatalf("approval does not show the exact edit: %+v", change)
		}

		approved = true

		return true, nil
	}

	var events []Event

	history, err := client.RunAgent(
		t.Context(),
		directory,
		nil,
		"Follow the brief",
		approve,
		func(event Event) {
			events = append(events, event)
		},
	)
	if err != nil || !approved || len(history) != 7 || len(events) != 5 {
		t.Fatalf(
			"agent turn: approval=%v history=%d events=%d error=%v",
			approved,
			len(history),
			len(events),
			err,
		)
	}

	data, _ := os.ReadFile(filepath.Join(directory, "greeting.md"))
	if string(data) != "Hello" {
		t.Fatal("approved agent output was not written")
	}
}

func TestAgentLoopBoundAndNoninteractiveEdits(t *testing.T) {
	directory := t.TempDir()
	client := mockClient(t, "deepseek", func(w http.ResponseWriter, _ *http.Request) {
		message := Message{
			Role:      "assistant",
			ToolCalls: []ToolCall{fileCall("write_file", "output.md", "Hello")},
		}
		json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": message}}})
	})

	history, err := client.RunAgent(t.Context(), directory, nil, "Create output", nil, func(Event) {})
	if err == nil || !errors.Is(err, ErrToolBatchPaused) {
		t.Fatalf("unbounded tool loop: %v", err)
	}

	if len(history) < 3 {
		t.Fatal("paused batch lost its tool history")
	}
	if _, err := os.Stat(filepath.Join(directory, "output.md")); !os.IsNotExist(err) {
		t.Fatal("noninteractive agent wrote a file")
	}

	client = mockClient(t, "deepseek", func(w http.ResponseWriter, _ *http.Request) {
		io.WriteString(
			w,
			`{"choices":[{"message":{"role":"assistant","content":"partial"},"finish_reason":"length"}]}`,
		)
	})
	if _, err := client.RunAgent(
		t.Context(),
		directory,
		nil,
		"Write output",
		nil,
		func(Event) {},
	); err == nil {
		t.Fatal("truncated completion must report an error")
	}
}
