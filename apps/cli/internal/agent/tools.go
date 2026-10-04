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
	Name            string                `json:"name"`
	Description     string                `json:"description"`
	Command         string                `json:"command"`
	ContractVersion string                `json:"contract_version"`
	Parameters      toolSchema            `json:"parameters"`
	FileSchemas     map[string]toolSchema `json:"file_schemas,omitempty"`
	OutputFormat    string                `json:"output_format"`
	OutputSchema    map[string]any        `json:"output_schema"`
}

func commandTool(name, description string, required []string, properties map[string]toolParameter) tool {
	if required == nil {
		required = []string{}
	}

	format := "json"
	if name == "events" || name == "run" {
		format = "ndjson"
	}

	var fileSchemas map[string]toolSchema
	if name == "create" {
		fileSchemas = map[string]toolSchema{"brief": createBriefSchema()}
	}

	return tool{
		Name: name, Description: description, Command: "agent " + name,
		ContractVersion: ContractVersion,
		Parameters:      toolSchema{Type: "object", Properties: properties, Required: required},
		FileSchemas:     fileSchemas,
		OutputFormat:    format,
		OutputSchema:    outputSchema(name),
	}
}

func createBriefSchema() toolSchema {
	filenames := toolParameter{
		Type: "array", Description: "Optional ordered list of up to 20 unique workspace filenames",
		Items: &toolParameter{Type: "string"},
	}

	return toolSchema{
		Type:     "object",
		Required: []string{"title", "description", "cost_lamports", "end_time", "level"},
		Properties: map[string]toolParameter{
			"title": {
				Type:        "string",
				Description: "Nonempty task title, up to 500 characters",
			},
			"description": {
				Type:        "string",
				Description: "Task instructions, up to 12000 characters and 32 KiB",
			},
			"acceptance_criteria": {
				Type:        "string",
				Description: "Optional plain-text expected result, up to 4000 characters",
			},
			"cost_lamports": {Type: "integer", Description: "Nonnegative budget in lamports"},
			"end_time": {
				Type:        "string",
				Description: "Future acceptance cutoff in RFC3339 format",
			},
			"level": {
				Type:        "string",
				Description: "Task difficulty",
				Enum:        []string{"easy", "medium", "complex"},
			},
			"input_files":      filenames,
			"expected_outputs": filenames,
			"funding_window_seconds": {
				Type:        "integer",
				Description: "Optional 60-2592000 seconds; provide all three timing fields together",
			},
			"deliver_by": {
				Type:        "string",
				Description: "Optional RFC3339 delivery date after end_time plus the funding window",
			},
			"review_window_seconds": {
				Type:        "integer",
				Description: "Optional 60-2592000 seconds; provide all three timing fields together",
			},
		},
	}
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
			"identity",
			"Read the account identity and any active task-scoped agent grant.",
			nil,
			nil,
		),
		commandTool(
			"grant",
			"Owner issues an accepted-task credential into a new private file. Read is always included.",
			[]string{"post", "name", "to"},
			map[string]toolParameter{
				"post": post,
				"name": {Type: "string", Description: "Harness name, up to 80 characters"},
				"to": {
					Type:        "string",
					Description: "New credential file path; existing files are never overwritten",
				},
				"expires-in": {Type: "string", Description: "Go duration from 1m to 168h; defaults to 1h"},
				"permission": {
					Type:        "array",
					Description: "Additional permissions (repeatable)",
					Items: &toolParameter{
						Type: "string",
						Enum: []string{"read", "message", "upload", "submit", "request-changes"},
					},
				},
			},
		),
		commandTool(
			"grants",
			"Owner lists their latest 100 grants for one task, including expiry and revocation.",
			[]string{"post"},
			map[string]toolParameter{"post": post},
		),
		commandTool(
			"revoke",
			"Owner revokes a grant by ID; repeated revocation returns the same revoked grant.",
			[]string{"grant-id"},
			map[string]toolParameter{
				"grant-id": {Type: "string", Description: "Agent grant UUID"},
			},
		),
		commandTool(
			"accept",
			"Claim an open task. Work waits for confirmed funding.",
			[]string{"post"},
			map[string]toolParameter{"post": post},
		),
		commandTool(
			"cancel",
			"Requester cancels an accepted, unfunded task. Active funding blocks recovery.",
			[]string{"post"},
			map[string]toolParameter{"post": post},
		),
		commandTool(
			"reopen",
			"Requester cancels an unfunded task and republishes its brief under a new ID, preserving private history.",
			[]string{"post"},
			map[string]toolParameter{
				"post": post,
				"end-time": {
					Type:        "string",
					Description: "Future RFC3339 acceptance cutoff; defaults to the original cutoff",
				},
				"deliver-by": {
					Type:        "string",
					Description: "Replacement RFC3339 delivery deadline; required when the copied deadline is too early",
				},
			},
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
			"Submit funded worker delivery. Use version and exact file/input IDs to reject stale work.",
			[]string{"post", "text"},
			map[string]toolParameter{
				"post": post,
				"text": {Type: "string", Description: "Delivery note"},
				"version": {
					Type:        "integer",
					Description: "Current workspace submission_version; 0 for first delivery. Omit only for legacy note submission",
				},
				"file-id": {
					Type:        "array",
					Description: "Exact uploaded output IDs; repeat --file-id. Requires version",
					Items:       &toolParameter{Type: "string"},
				},
				"input-id": {
					Type:        "array",
					Description: "Input snapshot IDs in declared filename order, or filename order when undeclared; repeat --input-id. Requires version",
					Items:       &toolParameter{Type: "string"},
				},
			},
		),
		commandTool(
			"request-changes",
			"Authorized reviewer requests a revision of the current submitted delivery; does not settle payment.",
			[]string{"post", "version", "text"},
			map[string]toolParameter{
				"post":    post,
				"version": {Type: "integer", Description: "Current submitted delivery version; at least 1"},
				"text":    {Type: "string", Description: "Changes required before approval"},
			},
		),
		commandTool(
			"events",
			"Stream workspace events as NDJSON, with automatic reconnect and replay. Run as a cancellable process.",
			[]string{"post"},
			map[string]toolParameter{"post": post, "after": after, "cursor": cursor},
		),
		commandTool(
			"history",
			"Read archived task events in pages; current live chat remains available through task/chat/events.",
			[]string{"post"},
			map[string]toolParameter{
				"post": post,
				"before": {
					Type:        "integer",
					Description: "Exclusive archived event ID; 0 starts at the newest page",
				},
				"limit": {Type: "integer", Description: "Page size from 1 to 100; defaults to 50"},
			},
		),
		commandTool(
			"wait",
			"Return one JSON result for an event, timeout, task closure, or required stream resynchronization.",
			[]string{"post"},
			map[string]toolParameter{
				"post":    post,
				"cursor":  cursor,
				"after":   after,
				"timeout": {Type: "string", Description: "Positive Go duration, at most 5m; defaults to 30s"},
				"event": {
					Type:        "array",
					Description: "Event kinds to match (repeatable); defaults to any event",
					Items:       &toolParameter{Type: "string"},
				},
			},
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
