package work

import "maps"

func prop(kind string) map[string]any  { return map[string]any{"type": kind} }
func array(kind string) map[string]any { return map[string]any{"type": "array", "items": prop(kind)} }
func object(fields map[string]any, required ...string) map[string]any {
	return map[string]any{"type": "object", "properties": fields, "required": required, "additionalProperties": false}
}
func Catalog() map[string]any {
	str := prop("string")
	integer := prop("integer")
	strings := array("string")
	evidence := object(map[string]any{"command": str, "exit_code": integer, "result": str, "environment": str}, "command", "exit_code", "result", "environment")
	spec := object(map[string]any{"id": str, "title": str, "instructions": str, "kind": map[string]any{"type": "string", "enum": []string{"work", "verification"}}, "assignee": integer, "dependencies": strings, "inputs": strings, "expected_outputs": strings}, "id", "title", "instructions", "assignee")
	fields := map[string]any{"name": str, "post_id": integer, "concurrency": integer, "assignment_id": str, "user_id": integer, "role": str, "assignments": map[string]any{"type": "array", "items": spec}, "attempt_id": str, "lease_seconds": integer, "contribution_id": str, "contributions": strings, "inputs": strings, "outputs": strings, "evidence": map[string]any{"type": "array", "items": evidence}, "base_revision": integer, "note": str, "reason": str, "next_action": str, "mode": str, "artifact_name": str, "source_file": str, "cost_lamports": integer, "end_time": map[string]any{"type": "string", "format": "date-time"}, "deliver_by": map[string]any{"type": "string", "format": "date-time"}, "funding_window_seconds": integer, "review_window_seconds": integer, "integration_id": str, "submission_version": integer, "permissions": strings, "allowed_members": array("integer"), "expires_in_seconds": integer, "grant_id": str, "usage": map[string]any{"type": "object", "additionalProperties": integer}}
	actionFields := map[string][]string{
		"create": {"name", "post_id", "concurrency"}, "invite": {"user_id", "role"}, "join": {}, "remove": {"user_id"}, "plan": {"assignments"}, "assign": {"assignment_id", "user_id"}, "claim": {"assignment_id", "lease_seconds"}, "renew": {"assignment_id", "attempt_id"}, "block": {"assignment_id", "attempt_id", "reason", "next_action"}, "fail": {"assignment_id", "attempt_id", "reason", "next_action"}, "submit": {"assignment_id", "attempt_id", "base_revision", "inputs", "outputs", "evidence", "note", "usage"}, "accept": {"assignment_id", "contribution_id"}, "retry": {"assignment_id"}, "cancel": {"assignment_id"}, "message": {"assignment_id", "note"}, "upload": {"assignment_id", "attempt_id", "artifact_name"}, "import": {"source_file"}, "grant": {"name", "permissions", "allowed_members", "expires_in_seconds"}, "revoke": {"grant_id"}, "control": {"mode"}, "integrate": {"base_revision", "contributions", "outputs", "note"}, "delegate": {"assignment_id", "cost_lamports", "end_time", "funding_window_seconds", "deliver_by", "review_window_seconds"}, "collect": {"assignment_id", "submission_version", "evidence", "usage"}, "deliver": {"integration_id", "submission_version", "inputs", "note"},
	}
	required := map[string][]string{"create": {"name"}, "invite": {"user_id"}, "remove": {"user_id"}, "plan": {"assignments"}, "assign": {"assignment_id", "user_id"}, "claim": {"assignment_id"}, "renew": {"assignment_id", "attempt_id"}, "block": {"assignment_id", "attempt_id", "reason", "next_action"}, "fail": {"assignment_id", "attempt_id", "reason", "next_action"}, "submit": {"assignment_id", "attempt_id", "base_revision", "inputs", "outputs"}, "accept": {"assignment_id", "contribution_id"}, "retry": {"assignment_id"}, "cancel": {"assignment_id"}, "message": {"assignment_id", "note"}, "import": {"source_file"}, "grant": {"name"}, "revoke": {"grant_id"}, "control": {"mode"}, "integrate": {"base_revision", "contributions", "outputs"}, "delegate": {"assignment_id", "cost_lamports", "end_time"}, "collect": {"assignment_id", "submission_version"}, "deliver": {"integration_id", "submission_version", "inputs"}}
	resultRequired := map[string][]string{
		"create": {"id", "owner_id", "name", "status", "revision", "cursor", "members", "assignments", "artifacts", "integrations"},
		"invite": {"user_id", "role", "status"}, "join": {"user_id", "role", "status"}, "remove": {"user_id", "role", "status"},
		"assign": {"id", "assignee", "state"}, "retry": {"id", "state"}, "cancel": {"id", "state"}, "block": {"id", "state", "blocker", "next_action"}, "fail": {"id", "state", "blocker", "next_action"},
		"claim": {"id", "operator_id", "lease_until", "state", "base_revision", "inputs", "dependencies"}, "renew": {"id", "lease_until", "state"},
		"submit": {"id", "assignment_id", "attempt_id", "operator_id", "base_revision", "inputs", "outputs", "dependencies", "evidence", "status"}, "accept": {"id", "assignment_id", "status"}, "collect": {"id", "assignment_id", "child_post", "child_version", "inputs", "outputs", "evidence", "status"},
		"upload": {"id", "name", "sha256", "size", "owner_id"}, "import": {"id", "name", "sha256", "size", "owner_id", "source_post", "source_file"},
		"integrate": {"id", "base_revision", "revision", "contributions", "outputs", "created_at"}, "delegate": {"post_id", "assignment_id", "funding_required", "parent_payment_unchanged"},
		"deliver": {"post_id", "submission_version", "delivery_files", "integration_id", "review_state"}, "grant": {"id", "project_id", "owner_id", "name", "permissions", "allowed_members", "expires_at"}, "revoke": {"grant_id", "status"}, "control": {"status", "outstanding_children"}, "message": {"actor_id", "text", "created_at"},
	}
	resultProperties := map[string]any{}
	for _, key := range []string{"id", "project_id", "name", "status", "state", "role", "assignment_id", "attempt_id", "grant_id", "sha256", "source_file", "integration_id", "review_state", "blocker", "next_action", "text"} {
		resultProperties[key] = str
	}
	for _, key := range []string{"user_id", "owner_id", "assignee", "operator_id", "actor_id", "post_id", "child_post", "child_version", "base_revision", "revision", "cursor", "submission_version", "size", "source_post"} {
		resultProperties[key] = integer
	}
	for _, key := range []string{"inputs", "outputs", "contributions", "delivery_files", "permissions"} {
		resultProperties[key] = strings
	}
	for _, key := range []string{"allowed_members", "outstanding_children"} {
		resultProperties[key] = array("integer")
	}
	for _, key := range []string{"members", "assignments", "artifacts", "integrations"} {
		resultProperties[key] = array("object")
	}
	for _, key := range []string{"created_at", "lease_until", "expires_at"} {
		resultProperties[key] = map[string]any{"type": "string", "format": "date-time"}
	}
	resultProperties["dependencies"] = map[string]any{"type": "object", "additionalProperties": str}
	resultProperties["evidence"] = map[string]any{"type": "array", "items": evidence}
	resultProperties["funding_required"] = prop("boolean")
	resultProperties["parent_payment_unchanged"] = prop("boolean")
	tools := []map[string]any{}
	for _, action := range append(append([]string{}, Actions...), "list", "show", "assignment", "handoff", "events", "download", "run") {
		params := map[string]any{}
		paramsRequired := []string{}
		if action != "list" && action != "create" {
			params["project"] = str
			paramsRequired = append(paramsRequired, "project")
		}
		output := map[string]any{"type": "object", "additionalProperties": true}
		fileSchemas := map[string]any{}
		if keys, ok := actionFields[action]; ok {
			resultFields := maps.Clone(resultProperties)
			switch action {
			case "assign", "retry", "cancel", "block", "fail":
				resultFields["dependencies"] = strings
				resultFields["contributions"] = array("object")
			}

			params["file"] = str
			params["operation-id"] = str
			requestProps := map[string]any{"operation_id": str, "action": map[string]any{"const": action}}
			for _, key := range keys {
				requestProps[key] = fields[key]
				switch key {
				case "user_id":
					params["user"] = map[string]any{"type": "integer", "minimum": 1}
				case "assignment_id":
					params["assignment"] = str
				case "attempt_id":
					params["attempt"] = str
				case "contribution_id":
					params["id"] = str
				case "grant_id":
					params["grant-id"] = str
				case "note", "reason":
					params["text"] = str
				}
			}
			fileSchemas["file"] = object(requestProps, append([]string{"operation_id"}, required[action]...)...)
			output["required"] = []string{"contract_version", "operation_id", "project_id", "cursor", "result"}
			output["properties"] = map[string]any{"contract_version": map[string]any{"const": "1"}, "operation_id": str, "project_id": str, "cursor": integer, "result": map[string]any{"type": "object", "properties": resultFields, "required": resultRequired[action], "additionalProperties": true}}
			if action == "plan" {
				output["properties"].(map[string]any)["result"] = map[string]any{"type": "array", "items": map[string]any{"type": "object", "required": []string{"id", "assignee", "state", "instructions"}, "additionalProperties": true}}
			}
			if action == "upload" {
				fileSchemas = map[string]any{}
				params["assignment"] = str
				params["attempt"] = str
				paramsRequired = append(paramsRequired, "file", "operation-id")
			}
			if action == "grant" {
				params["to"] = str
				paramsRequired = append(paramsRequired, "to")
				output["required"] = []string{"receipt", "credential_path"}
				output["properties"] = map[string]any{"receipt": map[string]any{"type": "object", "required": []string{"contract_version", "operation_id", "project_id", "cursor", "result"}, "additionalProperties": true}, "credential_path": str}
			}
		}
		switch action {
		case "list":
			output["required"] = []string{"projects"}
			output["properties"] = map[string]any{"projects": array("object")}
		case "show":
			output["required"] = []string{"project"}
			output["properties"] = map[string]any{"project": prop("object")}
		case "assignment", "handoff":
			params["assignment"] = str
			paramsRequired = append(paramsRequired, "assignment")
			if action == "assignment" {
				output["required"] = []string{"project_id", "project_name", "project_status", "revision", "cursor", "assignment", "available_artifacts"}
				output["properties"] = map[string]any{"project_id": str, "project_name": str, "project_status": str, "post_id": integer, "revision": integer, "cursor": integer, "assignment": prop("object"), "available_artifacts": array("object")}
			} else {
				output["required"] = []string{"project_id", "assignment_id", "assignee", "prompt"}
				output["properties"] = map[string]any{"project_id": str, "assignment_id": str, "assignee": integer, "prompt": str}
			}
		case "events":
			params["cursor"] = integer
			params["wait"] = map[string]any{"type": "integer", "minimum": 0, "maximum": 5}
			output["required"] = []string{"events", "cursor", "resync_required", "snapshot_command"}
			output["properties"] = map[string]any{"events": array("object"), "cursor": integer, "resync_required": prop("boolean"), "snapshot_command": str}
		case "download":
			params["id"] = str
			params["to"] = str
			paramsRequired = append(paramsRequired, "id", "to")
			output["required"] = []string{"artifact", "path"}
			output["properties"] = map[string]any{"artifact": map[string]any{"type": "object", "properties": resultProperties, "required": resultRequired["upload"], "additionalProperties": true}, "path": str}
		case "run":
			params["assignment"] = str
			params["operation-id"] = str
			params["exec"] = str
			params["arg"] = strings
			params["dir"] = str
			params["timeout"] = str
			paramsRequired = append(paramsRequired, "assignment", "operation-id", "exec", "dir")
			output["required"] = []string{"event"}
			output["properties"] = map[string]any{"event": str}
		}
		description := "Private project operation: " + action + ". Mutations use a stable operation ID; retries return the original receipt."
		if action == "delegate" {
			description = "Lead-only authorization of a private directed child task. Funding and settlement require separate explicit wallet signatures."
		}
		if action == "run" {
			description = "Explicitly connect and execute the operator's local harness in a fresh attempt directory, renew its lease, cancel on revocation and submit declared artifacts plus evidence.json."
		}
		if action == "assignment" {
			description = "Read one authorized assignment, its thread, attempts, contributions and available artifact metadata. Makes no claims, downloads or executions. Claim inputs are the authoritative execution snapshot."
		}
		if action == "handoff" {
			description = "Read an authorized assignment and produce a copyable Codex/Claude prompt with its server and IDs. Includes no credentials or file contents and never starts execution."
		}
		format := "json"
		if action == "run" {
			format = "ndjson"
		}
		tools = append(tools, map[string]any{"name": "work_" + action, "command": "work " + action, "description": description, "contract_version": "1", "parameters": object(params, paramsRequired...), "file_schemas": fileSchemas, "output_format": format, "output_schema": output})
	}
	return map[string]any{"contract_version": "1", "tools": tools, "errors": []string{"INVALID_ARGUMENT", "UNAUTHENTICATED", "FORBIDDEN", "NOT_FOUND", "CONFLICT", "UNAVAILABLE", "CANCELLED", "DEADLINE_EXCEEDED", "IO_ERROR", "INTERNAL"}}
}
