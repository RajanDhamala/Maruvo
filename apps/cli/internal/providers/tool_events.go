package providers

import (
	"encoding/json"
	"fmt"
	"strings"
	"unicode"
)

func toolDisplayText(value string) string {
	value = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}

		return r
	}, value)

	chars := []rune(value)
	if len(chars) > 240 {
		value = string(chars[:237]) + "…"
	}

	return value
}

func describeTool(call ToolCall, status, detail string) Event {
	var args struct {
		Path  string `json:"path"`
		Post  int64  `json:"post"`
		Query string `json:"query"`
		Level string `json:"level"`
		Title string `json:"title"`
		Name  string `json:"name"`
	}

	_ = json.Unmarshal([]byte(call.Function.Arguments), &args)

	label, target := call.Function.Name, ""
	switch label {
	case "list_files":
		label, target = "List directory", args.Path
	case "read_file":
		label, target = "Read file", args.Path
	case "write_file":
		label, target = "Write file", args.Path
	case "find_posts":
		label, target = "Search posts", args.Query
		if args.Level != "" {
			target += " (" + args.Level + ")"
		}
	case "my_posts":
		label, target = "Read my posts", args.Query
	case "get_task", "accept_post":
		label = "Read task"
		if call.Function.Name == "accept_post" {
			label = "Accept task"
		}

		target = fmt.Sprintf("post #%d", args.Post)
	case "create_post":
		label, target = "Create post", args.Title
	case "find_agents":
		label = "Find available agents"
	case "publish_offer":
		label, target = "Publish offer", args.Name
	case "get_workspace",
		"get_history",
		"send_message",
		"send_file",
		"receive_file",
		"wait_remote",
		"submit_delivery",
		"request_changes":
		label = map[string]string{"get_history": "Read remote history", "get_workspace": "Read remote workspace", "send_message": "Message remote agent", "send_file": "Share task file", "receive_file": "Receive task file", "wait_remote": "Wait for remote update", "submit_delivery": "Submit remote delivery", "request_changes": "Request remote revision"}[label]

		target = fmt.Sprintf("post #%d", args.Post)
		if args.Path != "" {
			target += " · " + args.Path
		}
	}

	symbol := "…"
	if status == "succeeded" {
		symbol = "✓"
	} else if status == "failed" {
		symbol = "×"
	}

	text := symbol + " " + toolDisplayText(label)
	if target != "" {
		text += " · " + toolDisplayText(target)
	}

	if detail != "" {
		text += "\n  " + toolDisplayText(detail)
	}

	return Event{Type: "tool", Text: text, ToolID: call.ID, Status: status}
}

func toolOutcome(name, result string) string {
	switch name {
	case "read_file":
		lines := strings.Count(result, "\n")
		if result != "" && !strings.HasSuffix(result, "\n") {
			lines++
		}

		label := "lines"
		if lines == 1 {
			label = "line"
		}

		return fmt.Sprintf("%d %s · %d bytes", lines, label, len(result))
	case "list_files":
		count := 0
		if result != "" {
			count = strings.Count(result, "\n") + 1
		}

		label := "entries"
		if count == 1 {
			label = "entry"
		}

		return fmt.Sprintf("%d %s", count, label)
	case "write_file":
		return "Saved after approval"
	case "create_post", "accept_post":
		var post struct {
			ID int64 `json:"id"`
		}

		_ = json.Unmarshal([]byte(result), &post)

		return fmt.Sprintf("post #%d", post.ID)
	default:
		return "Completed"
	}
}
