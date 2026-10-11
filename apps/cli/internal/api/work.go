package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"
)

type WorkMember struct {
	UserID int64  `json:"user_id"`
	Role   string `json:"role"`
	Status string `json:"status"`
}
type WorkArtifact struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	SHA256       string `json:"sha256"`
	Size         int64  `json:"size"`
	OwnerID      int64  `json:"owner_id"`
	AssignmentID string `json:"assignment_id,omitempty"`
	AttemptID    string `json:"attempt_id,omitempty"`
	SourcePost   int64  `json:"source_post,omitempty"`
	SourceFile   string `json:"source_file,omitempty"`
}
type WorkEvidence struct {
	Command     string `json:"command"`
	ExitCode    int    `json:"exit_code"`
	Result      string `json:"result"`
	Environment string `json:"environment"`
}
type WorkAttempt struct {
	ID           string            `json:"id"`
	OperatorID   int64             `json:"operator_id"`
	GrantID      string            `json:"grant_id,omitempty"`
	LeaseUntil   time.Time         `json:"lease_until"`
	State        string            `json:"state"`
	BaseRevision int64             `json:"base_revision"`
	Inputs       []string          `json:"inputs"`
	Dependencies map[string]string `json:"dependencies"`
	StartedAt    time.Time         `json:"started_at"`
}
type WorkContribution struct {
	ID           string            `json:"id"`
	AssignmentID string            `json:"assignment_id"`
	AttemptID    string            `json:"attempt_id"`
	OperatorID   int64             `json:"operator_id"`
	BaseRevision int64             `json:"base_revision"`
	Inputs       []string          `json:"inputs"`
	Outputs      []string          `json:"outputs"`
	Dependencies map[string]string `json:"dependencies"`
	Evidence     []WorkEvidence    `json:"evidence"`
	Note         string            `json:"note"`
	Status       string            `json:"status"`
	CreatedAt    time.Time         `json:"created_at"`
	Usage        map[string]int64  `json:"usage,omitempty"`
	ChildPost    int64             `json:"child_post,omitempty"`
	ChildVersion int64             `json:"child_version,omitempty"`
}
type WorkMessage struct {
	ActorID   int64     `json:"actor_id"`
	Text      string    `json:"text"`
	CreatedAt time.Time `json:"created_at"`
}
type WorkAssignment struct {
	ID              string             `json:"id"`
	Title           string             `json:"title"`
	Instructions    string             `json:"instructions"`
	Kind            string             `json:"kind"`
	Assignee        int64              `json:"assignee"`
	Dependencies    []string           `json:"dependencies"`
	Inputs          []string           `json:"inputs"`
	ExpectedOutputs []string           `json:"expected_outputs"`
	State           string             `json:"state"`
	Blocker         string             `json:"blocker"`
	NextAction      string             `json:"next_action"`
	Attempts        []WorkAttempt      `json:"attempts"`
	Contributions   []WorkContribution `json:"contributions"`
	Accepted        string             `json:"accepted_contribution,omitempty"`
	Thread          []WorkMessage      `json:"thread"`
	ChildPost       int64              `json:"child_post,omitempty"`
}
type WorkIntegration struct {
	ID            string    `json:"id"`
	BaseRevision  int64     `json:"base_revision"`
	Revision      int64     `json:"revision"`
	Contributions []string  `json:"contributions"`
	Outputs       []string  `json:"outputs"`
	Note          string    `json:"note"`
	CreatedAt     time.Time `json:"created_at"`
}
type WorkProject struct {
	ID                  string            `json:"id"`
	OwnerID             int64             `json:"owner_id"`
	PostID              int64             `json:"post_id,omitempty"`
	Name                string            `json:"name"`
	Status              string            `json:"status"`
	Concurrency         int               `json:"concurrency"`
	Revision            int64             `json:"revision"`
	Cursor              int64             `json:"cursor"`
	Members             []WorkMember      `json:"members"`
	Assignments         []WorkAssignment  `json:"assignments"`
	Artifacts           []WorkArtifact    `json:"artifacts"`
	Integrations        []WorkIntegration `json:"integrations"`
	OutstandingChildren []int64           `json:"outstanding_children"`
}

type WorkReceipt struct {
	ContractVersion string          `json:"contract_version"`
	OperationID     string          `json:"operation_id"`
	ProjectID       string          `json:"project_id"`
	Cursor          int64           `json:"cursor"`
	Result          json.RawMessage `json:"result"`
	Token           string          `json:"token,omitempty"`
}
type WorkEvent struct {
	ID           int64     `json:"id"`
	AssignmentID string    `json:"assignment_id"`
	Kind         string    `json:"kind"`
	ActorID      int64     `json:"actor_id"`
	GrantID      string    `json:"grant_id"`
	CreatedAt    time.Time `json:"created_at"`
}
type WorkEventBatch struct {
	Events          []WorkEvent `json:"events"`
	Cursor          int64       `json:"cursor"`
	ResyncRequired  bool        `json:"resync_required"`
	SnapshotCommand string      `json:"snapshot_command"`
}

func (c *Client) WorkProjects(ctx context.Context, token string) ([]WorkProject, error) {
	var result struct {
		Projects []WorkProject `json:"projects"`
	}
	err := c.request(ctx, "GET", "/work/projects", token, nil, &result)
	return result.Projects, err
}
func (c *Client) WorkProject(ctx context.Context, token, project string) (WorkProject, error) {
	var result struct {
		Project WorkProject `json:"project"`
	}
	err := c.request(ctx, "GET", "/work/projects/"+url.PathEscape(project), token, nil, &result)
	return result.Project, err
}
func (c *Client) WorkOperation(ctx context.Context, token, project string, request any) (WorkReceipt, error) {
	var result WorkReceipt
	data, err := json.Marshal(request)
	if err != nil {
		return result, err
	}
	path := "/work/projects"
	if project != "" {
		path += "/" + url.PathEscape(project) + "/operations"
	}
	err = c.request(ctx, "POST", path, token, bytes.NewReader(data), &result)
	return result, err
}
func (c *Client) WorkEvents(ctx context.Context, token, project string, cursor int64, wait int) (WorkEventBatch, error) {
	var result WorkEventBatch
	path := fmt.Sprintf("/work/projects/%s/events?cursor=%d&wait=%d", url.PathEscape(project), cursor, wait)
	err := c.request(ctx, "GET", path, token, nil, &result)
	return result, err
}
func (c *Client) WorkDownload(ctx context.Context, token, project, id string) ([]byte, error) {
	path := "/work/projects/" + url.PathEscape(project) + "/artifacts/" + url.PathEscape(id)
	req, err := http.NewRequestWithContext(ctx, "GET", c.baseURL+path, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	response, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		detail, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
		return nil, &Error{response.StatusCode, string(detail)}
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, (10<<20)+1))
	if err == nil && len(data) > 10<<20 {
		return nil, fmt.Errorf("artifact exceeds 10 MiB")
	}
	return data, err
}
