package controller

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func TestManualControlRevokesOnlyOwnAccessAndResumeNeedsFreshCredentials(t *testing.T) {
	f := newAgentAccessFixture(t)
	path := fmt.Sprintf("/posts/%d/agent-control", f.postID)
	grantPath := fmt.Sprintf("/posts/%d/agents", f.postID)
	messagePath := fmt.Sprintf("/posts/%d/messages", f.postID)
	requester, _ := f.grant(t, 0, "read", "message")
	worker, _ := f.grant(t, 1, "read", "message")

	f.request(t, "GET", path, requester, nil, 403)
	f.request(t, "PUT", path, requester, map[string]string{"mode": "agent"}, 403)
	f.request(t, "PUT", path, f.tokens[2], map[string]string{"mode": "manual"}, 403)
	f.request(t, "PUT", path, f.tokens[0], map[string]string{"mode": "unknown"}, 400)

	var control agentControlView

	body := f.request(t, "PUT", path, f.tokens[0], map[string]string{"mode": "manual"}, 200)
	if json.Unmarshal(body, &control) != nil || control.Mode != "manual" || control.RevokedGrants != 1 {
		t.Fatalf("takeover did not revoke owned grants: %s", body)
	}

	f.request(t, "POST", messagePath, requester, map[string]string{"text": "agent reply"}, 401)
	f.request(t, "POST", messagePath, worker, map[string]string{"text": "worker still working"}, 201)
	f.request(t, "POST", messagePath, f.tokens[0], map[string]string{"text": "human answer"}, 201)
	f.request(t, "POST", grantPath, f.tokens[0], map[string]any{
		"name": "new callback", "permissions": []string{"read"}, "expires_in_seconds": 3600,
	}, 409)

	body = f.request(t, "GET", path, f.tokens[1], nil, 200)
	if json.Unmarshal(body, &control) != nil || control.Mode != "agent" {
		t.Fatal("requester takeover changed worker control")
	}

	f.request(t, "PUT", path, f.tokens[0], map[string]string{"mode": "agent"}, 200)
	f.request(t, "POST", messagePath, requester, map[string]string{"text": "old credential"}, 401)
	fresh, _ := f.grant(t, 0, "read", "message")
	f.request(t, "POST", messagePath, fresh, map[string]string{"text": "fresh credential"}, 201)

	var events int
	if err := f.pool.QueryRow(f.ctx, "SELECT count(*) FROM workspace_events WHERE post_id=$1 AND kind='agent.control'",
		f.postID).
		Scan(&events); err != nil ||
		events != 2 {
		t.Fatalf("missing durable takeover/resume history: %d, %v", events, err)
	}

	f.request(t, "PUT", path, f.tokens[1], map[string]string{"mode": "manual"}, 200)
	activityPath := fmt.Sprintf("/posts/%d/activity", f.postID)
	f.request(t, "POST", activityPath, f.tokens[1], map[string]string{
		"run_id": strings.Repeat("r", 32), "state": "working", "detail": "Executing",
	}, 409)
	f.request(t, "POST", activityPath, f.tokens[1], map[string]string{
		"run_id": strings.Repeat("r", 32), "state": "interrupted", "detail": "Human took over",
	}, 201)
}
