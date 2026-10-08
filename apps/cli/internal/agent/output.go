package agent

func objectSchema(properties map[string]any, required ...string) map[string]any {
	return map[string]any{"type": "object", "properties": properties, "required": required}
}

func outputSchema(name string) map[string]any {
	integer := map[string]any{"type": "integer"}
	text := map[string]any{"type": "string"}
	strings := map[string]any{"type": "array", "items": text}
	remote := objectSchema(
		map[string]any{
			"status":             text,
			"agent_name":         text,
			"worker_online":      map[string]any{"type": "boolean"},
			"worker_seen":        text,
			"detail":             text,
			"submission_version": integer,
			"last_event_id":      integer,
		},
		"status",
		"agent_name",
		"worker_online",
		"detail",
		"submission_version",
		"last_event_id",
	)
	post := objectSchema(
		map[string]any{
			"id":            integer,
			"status":        text,
			"target_worker": map[string]any{"type": []string{"integer", "null"}},
			"remote":        remote,
		},
		"id",
		"status",
	)
	file := objectSchema(map[string]any{
		"id": text, "post_id": integer, "name": text, "purpose": text, "size": integer, "sha256": text,
	}, "id", "post_id", "name", "purpose", "size", "sha256")
	message := objectSchema(map[string]any{"message_id": text, "stream_id": text}, "message_id", "stream_id")
	grant := objectSchema(map[string]any{
		"id": text, "owner_id": integer, "post_id": integer, "name": text,
		"permissions": strings, "expires_at": text,
	}, "id", "owner_id", "post_id", "name", "permissions", "expires_at")
	context := objectSchema(map[string]any{
		"role":                   text,
		"terminal":               map[string]any{"type": "boolean"},
		"submission_version":     integer,
		"allowed_actions":        strings,
		"blocked_actions":        map[string]any{"type": "object", "additionalProperties": text},
		"upload_purposes":        strings,
		"waiting_for":            strings,
		"input_file_ids":         strings,
		"missing_inputs":         strings,
		"missing_outputs":        strings,
		"human_payment_required": map[string]any{"type": "boolean"},
	}, "role", "terminal", "submission_version", "allowed_actions", "blocked_actions", "upload_purposes",
		"waiting_for", "input_file_ids", "missing_inputs", "missing_outputs", "human_payment_required")
	offer := objectSchema(map[string]any{
		"user_id": integer, "name": text, "description": text, "capabilities": strings,
		"min_lamports": integer, "job_timeout_seconds": integer,
		"available":       map[string]any{"type": "boolean"},
		"online":          map[string]any{"type": "boolean"},
		"available_until": map[string]any{"type": []string{"string", "null"}}, "updated_at": text,
	}, "user_id", "name", "description", "capabilities", "min_lamports", "job_timeout_seconds", "available", "updated_at")

	switch name {
	case "control":
		return objectSchema(map[string]any{
			"post_id": integer, "owner_id": integer, "mode": text,
			"updated_at": text, "revoked_grants": integer,
		}, "post_id", "owner_id", "mode", "revoked_grants")
	case "connection":
		return objectSchema(
			map[string]any{"executable": text, "directory": text, "argument_count": integer, "status": text},
			"executable",
			"directory",
			"argument_count",
			"status",
		)
	case "connect":
		return objectSchema(
			map[string]any{
				"status": text,
				"offer":  map[string]any{"oneOf": []any{offer, map[string]any{"type": "null"}}},
				"next":   text,
			},
			"status",
			"offer",
			"next",
		)
	case "activity":
		return objectSchema(map[string]any{"status": text}, "status")
	case "offer":
		return offer
	case "offers":
		return map[string]any{"type": "array", "items": offer}
	case "history":
		return objectSchema(map[string]any{
			"post_id":     integer,
			"events":      map[string]any{"type": "array", "items": map[string]any{"type": "object"}},
			"has_more":    map[string]any{"type": "boolean"},
			"next_before": integer,
		}, "post_id", "events", "has_more", "next_before")
	case "wait":
		return objectSchema(map[string]any{
			"status": map[string]any{
				"type": "string",
				"enum": []string{"event", "timeout", "closed", "resync"},
			},
			"post_id": integer,
			"cursor":  text,
			"event":   map[string]any{"type": "object"},
		}, "status", "post_id", "cursor")
	case "identity":
		return objectSchema(
			map[string]any{"id": text, "username": text, "agent_access": grant},
			"id",
			"username",
		)
	case "grant":
		return objectSchema(
			map[string]any{"grant": grant, "credential_path": text},
			"grant",
			"credential_path",
		)
	case "grants":
		return map[string]any{"type": "array", "items": grant}
	case "revoke":
		return grant
	case "feed", "tasks", "inbox":
		return map[string]any{"type": []string{"array", "null"}, "items": post}
	case "create", "accept", "cancel", "reopen":
		return post
	case "task":
		return objectSchema(map[string]any{
			"post": post, "escrow": map[string]any{"type": "object"},
			"workspace": map[string]any{"type": "object"}, "can_review": map[string]any{"type": "boolean"},
			"context": context,
		}, "post", "escrow")
	case "chat":
		return map[string]any{"oneOf": []any{
			message,
			objectSchema(map[string]any{
				"post_id": integer, "last_event_id": integer, "cursor": text,
				"messages": map[string]any{"type": "array", "items": map[string]any{"type": "object"}},
			}, "post_id", "last_event_id", "cursor", "messages"),
		}}
	case "files":
		return map[string]any{"type": []string{"array", "null"}, "items": file}
	case "send-file":
		return file
	case "submit", "request-changes":
		return objectSchema(map[string]any{
			"event_id": integer, "post_id": integer, "submission_version": integer,
			"review_state": text, "delivery_files": strings,
		}, "event_id", "post_id", "submission_version", "review_state")
	case "events":
		return objectSchema(map[string]any{"event": text, "data": map[string]any{}}, "event", "data")
	case "run", "serve", "listen":
		return objectSchema(map[string]any{"event": text}, "event")
	default:
		return objectSchema(map[string]any{"status": text}, "status")
	}
}
