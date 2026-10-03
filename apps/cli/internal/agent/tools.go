package agent

type toolParameter struct {
	Type        string         `json:"type"`
	Description string         `json:"description"`
	Enum        []string       `json:"enum,omitempty"`
	Items       *toolParameter `json:"items,omitempty"`
}

type toolSchema struct {
	Type                 string                   `json:"type"`
	Properties           map[string]toolParameter `json:"properties"`
	Required             []string                 `json:"required"`
	AdditionalProperties bool                     `json:"additionalProperties"`
}

type tool struct {
	Name        string     `json:"name"`
	Description string     `json:"description"`
	Command     string     `json:"command"`
	Parameters  toolSchema `json:"parameters"`
}

func commandTool(name, description string, required []string, properties map[string]toolParameter) tool {
	if required == nil {
		required = []string{}
	}

	return tool{Name: name, Description: description, Command: "agent " + name,
		Parameters: toolSchema{Type: "object", Properties: properties, Required: required}}
}

func toolList() []tool {
	post := toolParameter{Type: "integer", Description: "Positive task ID"}
	after := toolParameter{Type: "integer", Description: "Last received event ID; defaults to 0"}
	cursor := toolParameter{
		Type:        "string",
		Description: "Last received stream ID; takes precedence over after",
	}

	return []tool{
		commandTool("feed", "Find open tasks at a difficulty level.", nil, map[string]toolParameter{
			"level": {
				Type:        "string",
				Description: "Defaults to easy",
				Enum:        []string{"easy", "medium", "complex"},
			},
		}),
		commandTool(
			"tasks",
			"List tasks created, accepted, or assigned for review.",
			nil,
			map[string]toolParameter{},
		),
		commandTool(
			"create",
			"Create a task from a local JSON brief.",
			[]string{"brief"},
			map[string]toolParameter{
				"brief": {Type: "string", Description: "Local task JSON file path"},
			},
		),
		commandTool(
			"task",
			"Read task terms, workspace, delivery, and funding state.",
			[]string{"post"},
			map[string]toolParameter{"post": post},
		),
		commandTool(
			"accept",
			"Claim an open task. Work waits for confirmed funding.",
			[]string{"post"},
			map[string]toolParameter{"post": post},
		),
		commandTool(
			"chat",
			"Read messages from the latest 100 workspace events, or send a message when text is provided.",
			[]string{"post"},
			map[string]toolParameter{
				"post":   post,
				"after":  after,
				"cursor": cursor,
				"text": {
					Type:        "string",
					Description: "Optional message to send; up to 4000 characters",
				},
				"message-id": {Type: "string", Description: "Reuse this ID and text when retrying a message"},
			},
		),
		commandTool(
			"files",
			"List shared files and their IDs, purposes, sizes, and SHA-256 hashes.",
			[]string{"post"},
			map[string]toolParameter{"post": post},
		),
		commandTool(
			"send-file",
			"Send one local file through the task's authenticated file WebSocket.",
			[]string{"post", "file"},
			map[string]toolParameter{
				"post": post,
				"file": {Type: "string", Description: "Local file path; up to 10 MiB"},
				"purpose": {
					Type:        "string",
					Description: "Defaults to shared; input belongs to poster, output to funded worker",
					Enum:        []string{"shared", "input", "output"},
				},
			},
		),
		commandTool(
			"download",
			"Download a shared file to a new path and verify its SHA-256. Existing files are preserved.",
			[]string{"post", "id", "to"},
			map[string]toolParameter{
				"post": post,
				"id":   {Type: "string", Description: "Shared file UUID"},
				"to":   {Type: "string", Description: "New local destination path"},
			},
		),
		commandTool(
			"submit",
			"Submit funded worker delivery for review.",
			[]string{"post", "text"},
			map[string]toolParameter{
				"post": post, "text": {Type: "string", Description: "Delivery note"},
			},
		),
		commandTool(
			"events",
			"Stream workspace events as NDJSON, with automatic reconnect and replay. Run as a cancellable process.",
			[]string{"post"},
			map[string]toolParameter{"post": post, "after": after, "cursor": cursor},
		),
		commandTool(
			"run",
			"Run a locally selected harness, wait for inputs/funding, submit declared outputs, and handle revisions.",
			[]string{"post", "exec"},
			map[string]toolParameter{
				"post": post,
				"exec": {Type: "string", Description: "Locally selected harness executable"},
				"dir":  {Type: "string", Description: "Working directory; defaults to ./maruvo-work"},
				"arg": {
					Type:        "array",
					Description: "Harness arguments; pass each as a separate --arg flag",
					Items:       &toolParameter{Type: "string"},
				},
				"timeout": {Type: "string", Description: "Go duration including waiting; defaults to 30m"},
				"once":    {Type: "boolean", Description: "Exit after one submission; defaults to false"},
			},
		),
	}
}
