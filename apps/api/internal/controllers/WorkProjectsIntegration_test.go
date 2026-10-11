package controller

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/rajandhamala/Maruvo/internal/work"
)

func workFixture(t *testing.T) *agentAccessFixture {
	f := newAgentAccessFixture(t)
	original := f.server
	mux := http.NewServeMux()
	mux.Handle("/", original.Config.Handler)
	f.server = httptest.NewServer(mux)
	t.Cleanup(f.server.Close)
	c := f.controller
	mux.HandleFunc("GET /work/projects", c.Auth(c.WorkProjects))
	mux.HandleFunc("POST /work/projects", c.Auth(c.WorkProjects))
	mux.HandleFunc("GET /work/projects/{project}", c.Auth(c.WorkProject))
	mux.HandleFunc("POST /work/projects/{project}/operations", c.Auth(c.WorkOperation))
	mux.HandleFunc("GET /work/projects/{project}/artifacts/{artifact}", c.Auth(c.WorkDownload))
	mux.HandleFunc("GET /work/projects/{project}/events", c.Auth(c.WorkEvents))
	mux.HandleFunc("POST /posts/feed", c.Auth(c.DescLevelPost))
	return f
}
func workDecode[T any](t *testing.T, data []byte) T {
	t.Helper()
	var result T
	if err := json.Unmarshal(data, &result); err != nil {
		t.Fatal(err)
	}
	return result
}

type workTestReceipt struct {
	ProjectID string          `json:"project_id"`
	Cursor    int64           `json:"cursor"`
	Result    json.RawMessage `json:"result"`
	Token     string          `json:"token"`
}

func (f *agentAccessFixture) workOp(t *testing.T, owner int, p string, r work.Request, status int) workTestReceipt {
	t.Helper()
	if r.OperationID == "" {
		r.OperationID = uuid.NewString()
	}
	path := "/work/projects"
	if p != "" {
		path += "/" + p + "/operations"
	}
	return workDecode[workTestReceipt](t, f.request(t, "POST", path, f.tokens[owner], r, status))
}
func (f *agentAccessFixture) workView(t *testing.T, owner int, p string) work.Project {
	t.Helper()
	view := workDecode[struct {
		Project work.Project `json:"project"`
	}](t, f.request(t, "GET", "/work/projects/"+p, f.tokens[owner], nil, 200))
	return view.Project
}
func (f *agentAccessFixture) workTeam(t *testing.T, lead int, p string, members ...int) {
	t.Helper()
	for _, i := range members {
		f.workOp(t, lead, p, work.Request{Action: "invite", UserID: f.users[i]}, 200)
		f.workOp(t, i, p, work.Request{Action: "join"}, 200)
	}
}
func (f *agentAccessFixture) workUpload(t *testing.T, owner int, p, s, attempt, name, content string) work.Artifact {
	t.Helper()
	receipt := f.workOp(t, owner, p, work.Request{Action: "upload", AssignmentID: s, AttemptID: attempt, ArtifactName: name, Content: []byte(content)}, 200)
	return workDecode[work.Artifact](t, receipt.Result)
}
func (f *agentAccessFixture) workSubmit(t *testing.T, owner int, p, s string, at work.Attempt, outputs []string) work.Contribution {
	t.Helper()
	receipt := f.workOp(t, owner, p, work.Request{Action: "submit", AssignmentID: s, AttemptID: at.ID, BaseRevision: at.BaseRevision, Inputs: at.Inputs, Outputs: outputs, Note: "checked", Evidence: []work.Evidence{{Command: "go test ./...", ExitCode: 0, Result: "PASS", Environment: "independent fixture runtime"}}}, 200)
	return workDecode[work.Contribution](t, receipt.Result)
}
func (f *agentAccessFixture) workAccept(t *testing.T, owner int, p, s, id string) {
	t.Helper()
	f.workOp(t, owner, p, work.Request{Action: "accept", AssignmentID: s, ContributionID: id}, 200)
}
func TestWorkPatchVerificationIntegrationAndRestart(t *testing.T) {
	f := workFixture(t)
	created := f.workOp(t, 0, "", work.Request{Name: "Independent bug verification"}, 200)
	p := created.ProjectID
	f.workTeam(t, 0, p, 1, 2)
	input := f.workUpload(t, 0, p, "", "", "source.txt", "source snapshot")
	unshared := f.workUpload(t, 0, p, "", "", "private.txt", "owner secret")
	plan := work.Request{Action: "plan", Assignments: []work.AssignmentSpec{{ID: "patch", Title: "Patch", Instructions: "Fix the bug", Assignee: f.users[1], Inputs: []string{input.ID}, ExpectedOutputs: []string{"patch.diff"}}, {ID: "verify", Title: "Verify", Instructions: "Reproduce and test the exact patch", Kind: "verification", Assignee: f.users[2], Dependencies: []string{"patch"}, ExpectedOutputs: []string{"tests.log"}}}}
	f.workOp(t, 0, p, plan, 200)
	f.request(t, "GET", "/work/projects/"+p+"/artifacts/"+unshared.ID, f.tokens[1], nil, 403)
	f.request(t, "GET", "/work/projects/"+p+"/artifacts/"+input.ID, f.tokens[2], nil, 403)
	f.workOp(t, 2, p, work.Request{Action: "claim", AssignmentID: "verify"}, 409)
	at := workDecode[work.Attempt](t, f.workOp(t, 1, p, work.Request{Action: "claim", AssignmentID: "patch"}, 200).Result)
	patch := f.workUpload(t, 1, p, "patch", at.ID, "patch.diff", "exact patch v1")
	cp := f.workSubmit(t, 1, p, "patch", at, []string{patch.ID})
	f.workAccept(t, 0, p, "patch", cp.ID)
	dependencyEvents := f.request(t, "GET", "/work/projects/"+p+"/events?cursor=0", f.tokens[2], nil, 200)
	if !bytes.Contains(dependencyEvents, []byte(`"kind":"accept"`)) || bytes.Contains(dependencyEvents, []byte("Fix the bug")) {
		t.Fatal("verification did not receive a private dependency wakeup")
	}
	va := workDecode[work.Attempt](t, f.workOp(t, 2, p, work.Request{Action: "claim", AssignmentID: "verify"}, 200).Result)
	if va.Dependencies["patch"] != cp.ID || !slices.Contains(va.Inputs, patch.ID) {
		t.Fatal("verification did not pin patch artifact and contribution")
	}
	report := f.workUpload(t, 2, p, "verify", va.ID, "tests.log", "PASS exact patch v1")
	verification := f.workSubmit(t, 2, p, "verify", va, []string{report.ID})
	f.workAccept(t, 0, p, "verify", verification.ID)
	workerView := f.workView(t, 1, p)
	if len(workerView.Assignments) != 1 || workerView.Assignments[0].ID != "patch" {
		t.Fatal("contributor read another operator's assignment")
	}
	f.workOp(t, 0, p, work.Request{Action: "integrate", Contributions: []string{cp.ID}, Outputs: []string{patch.ID}}, 409)
	integrated := workDecode[work.Integration](t, f.workOp(t, 0, p, work.Request{Action: "integrate", Contributions: []string{cp.ID, verification.ID}, Outputs: []string{patch.ID, report.ID}, Note: "Reviewed patch and independent proof"}, 200).Result)
	if integrated.Revision != 1 {
		t.Fatal("integration did not advance revision")
	}
	// A fresh controller reconstructs everything from PostgreSQL, without harness conversation state.
	restarted := NewController(f.pool, f.controller.rpc, nil, f.controller.redis)
	mux := http.NewServeMux()
	mux.HandleFunc("GET /work/projects/{project}", restarted.Auth(restarted.WorkProject))
	server := httptest.NewServer(mux)
	defer server.Close()
	req, _ := http.NewRequestWithContext(f.ctx, "GET", server.URL+"/work/projects/"+p, nil)
	req.Header.Set("Authorization", "Bearer "+f.tokens[0])
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	data, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	restored := workDecode[struct {
		Project work.Project `json:"project"`
	}](t, data).Project
	if restored.Revision != 1 || len(restored.Artifacts) != 4 || len(restored.Integrations) != 1 {
		t.Fatal("restart lost work/artifact history")
	}
}
func TestWorkCompetingClaimsExpiryReceiptsAndRevocation(t *testing.T) {
	f := workFixture(t)
	p := f.workOp(t, 0, "", work.Request{Name: "Leased work"}, 200).ProjectID
	f.workTeam(t, 0, p, 1)
	plan := work.Request{Action: "plan", OperationID: "same-plan", Assignments: []work.AssignmentSpec{{ID: "one", Title: "One", Instructions: "Bounded work", Assignee: f.users[1]}}}
	original := f.workOp(t, 0, p, plan, 200)
	repeat := f.workOp(t, 0, p, plan, 200)
	if !reflect.DeepEqual(workDecode[any](t, original.Result), workDecode[any](t, repeat.Result)) || original.Cursor != repeat.Cursor {
		t.Fatal("retry changed receipt")
	}
	plan.Assignments[0].Title = "different"
	f.workOp(t, 0, p, plan, 409)
	path := "/work/projects/" + p + "/operations"
	results := make(chan int, 2)
	var wg sync.WaitGroup
	for range 2 {
		wg.Go(func() {
			payload, _ := json.Marshal(work.Request{Action: "claim", OperationID: uuid.NewString(), AssignmentID: "one"})
			r, _ := http.NewRequestWithContext(f.ctx, "POST", f.server.URL+path, bytes.NewReader(payload))
			r.Header.Set("Authorization", "Bearer "+f.tokens[1])
			res, err := http.DefaultClient.Do(r)
			if err != nil {
				results <- 0
				return
			}
			defer res.Body.Close()
			_, _ = io.Copy(io.Discard, res.Body)
			results <- res.StatusCode
		})
	}
	wg.Wait()
	close(results)
	statuses := []int{}
	for status := range results {
		statuses = append(statuses, status)
	}
	slices.Sort(statuses)
	if !slices.Equal(statuses, []int{200, 409}) {
		t.Fatalf("competing claims: %v", statuses)
	}
	view := f.workView(t, 0, p)
	old := *view.Assignment("one").CurrentAttempt()
	view.Assignment("one").CurrentAttempt().LeaseUntil = time.Now().Add(-time.Second)
	state, _ := json.Marshal(view)
	if _, err := f.pool.Exec(f.ctx, "UPDATE work_projects SET state=$2 WHERE id=$1", p, state); err != nil {
		t.Fatal(err)
	}
	fresh := workDecode[work.Attempt](t, f.workOp(t, 1, p, work.Request{Action: "claim", AssignmentID: "one"}, 200).Result)
	if old.ID == fresh.ID {
		t.Fatal("expired attempt reused")
	}
	f.workOp(t, 1, p, work.Request{Action: "submit", AssignmentID: "one", AttemptID: old.ID}, 409)
	f.workOp(t, 1, p, work.Request{Action: "block", AssignmentID: "one", AttemptID: fresh.ID, Reason: "tool unavailable", NextAction: "operator restores compiler"}, 200)
	f.workOp(t, 0, p, work.Request{Action: "retry", AssignmentID: "one"}, 200)
	grant := f.workOp(t, 1, p, work.Request{Action: "grant", Name: "runtime", Permissions: []string{"execute", "upload", "message"}}, 200)
	g := workDecode[workGrant](t, grant.Result)
	claim := work.Request{Action: "claim", OperationID: "grant-claim", AssignmentID: "one"}
	body := f.request(t, "POST", path, grant.Token, claim, 200)
	claimReceipt := workDecode[workTestReceipt](t, body)
	_ = claimReceipt
	// Replayed mutations still require the credential's current permissions.
	readGrant := f.workOp(t, 1, p, work.Request{Action: "grant", Name: "read only"}, 200)
	f.request(t, "POST", path, readGrant.Token, claim, 403)
	f.request(t, "POST", "/posts/fund", grant.Token, map[string]any{"id": f.postID}, 403)
	f.workOp(t, 1, p, work.Request{Action: "revoke", GrantID: g.ID}, 200)
	f.request(t, "GET", "/work/projects/"+p, grant.Token, nil, 401)
	f.request(t, "POST", path, grant.Token, work.Request{Action: "submit", OperationID: "revoked-submit", AssignmentID: "one"}, 401)
	var stored string
	if err := f.pool.QueryRow(f.ctx, "SELECT token_hash FROM work_grants WHERE id=$1", g.ID).Scan(&stored); err != nil || stored != tokenHash(grant.Token) {
		t.Fatal("credential hash mismatch")
	}
	var secretStored bool
	if err := f.pool.QueryRow(f.ctx, "SELECT EXISTS(SELECT 1 FROM work_receipts WHERE response::text LIKE $1)", "%"+grant.Token+"%").Scan(&secretStored); err != nil || secretStored {
		t.Fatal("receipt stored a credential secret")
	}
}
func TestWorkCycleStaleVerificationAndCoordinatorLimits(t *testing.T) {
	f := workFixture(t)
	p := f.workOp(t, 0, "", work.Request{Name: "Stale verification"}, 200).ProjectID
	f.workTeam(t, 0, p, 1, 2)
	f.workOp(t, 0, p, work.Request{Action: "plan", Assignments: []work.AssignmentSpec{{ID: "a", Title: "a", Instructions: "a", Assignee: f.users[1], Dependencies: []string{"b"}}, {ID: "b", Title: "b", Instructions: "b", Assignee: f.users[2], Dependencies: []string{"a"}}}}, 400)
	if len(f.workView(t, 0, p).Assignments) != 0 {
		t.Fatal("invalid graph committed")
	}
	f.workOp(t, 0, p, work.Request{Action: "plan", Assignments: []work.AssignmentSpec{{ID: "patch", Title: "Patch", Instructions: "fix", Assignee: f.users[1]}, {ID: "verify", Title: "Verify", Instructions: "test", Kind: "verification", Assignee: f.users[2], Dependencies: []string{"patch"}}}}, 200)
	at := workDecode[work.Attempt](t, f.workOp(t, 1, p, work.Request{Action: "claim", AssignmentID: "patch"}, 200).Result)
	cp := f.workSubmit(t, 1, p, "patch", at, nil)
	f.workAccept(t, 0, p, "patch", cp.ID)
	va := workDecode[work.Attempt](t, f.workOp(t, 2, p, work.Request{Action: "claim", AssignmentID: "verify"}, 200).Result)
	f.workOp(t, 0, p, work.Request{Action: "retry", AssignmentID: "patch"}, 200)
	f.workOp(t, 2, p, work.Request{Action: "submit", AssignmentID: "verify", AttemptID: va.ID, Inputs: va.Inputs, Evidence: []work.Evidence{{Command: "test", Result: "PASS", Environment: "other device"}}}, 409)
	view := f.workView(t, 0, p)
	if view.Assignment("verify").State != "blocked" {
		t.Fatal("retry did not invalidate verification")
	}
	grant := f.workOp(t, 0, p, work.Request{Action: "grant", Name: "coordinator", Permissions: []string{"manage"}, AllowedMembers: []int64{f.users[1]}}, 200)
	f.request(t, "POST", "/work/projects/"+p+"/operations", grant.Token, work.Request{Action: "plan", OperationID: "outside-team", Assignments: []work.AssignmentSpec{{ID: "other", Title: "Other", Instructions: "no", Assignee: f.users[2]}}}, 403)
	f.workOp(t, 0, p, work.Request{Action: "control", Mode: "paused"}, 200)
	f.request(t, "GET", "/work/projects/"+p, grant.Token, nil, 401)
	f.workOp(t, 1, p, work.Request{Action: "claim", AssignmentID: "patch"}, 409)
}
func TestWorkPrivateChildFundingProvenanceAndIndependentSettlement(t *testing.T) {
	f := workFixture(t)
	p := f.workOp(t, 1, "", work.Request{Name: "Paid integration", PostID: f.postID}, 200).ProjectID
	f.workTeam(t, 1, p, 2)
	f.workUpload(t, 1, p, "", "", "unshared-secret-name.txt", "private project input")
	f.workOp(t, 1, p, work.Request{Action: "plan", Assignments: []work.AssignmentSpec{{ID: "patch", Title: "Private patch", Instructions: "Private child instructions", Assignee: f.users[2], ExpectedOutputs: []string{"patch.diff"}}}}, 200)
	f.workOp(t, 2, p, work.Request{Action: "claim", AssignmentID: "patch"}, 409)
	// Confirm funding in the disposable fixture; no live transaction is signed by this test.
	fund := func(post int64) {
		t.Helper()
		_, err := f.pool.Exec(f.ctx, `INSERT INTO post_escrows(post_id,address,program_id,reviewer,network,transaction,last_valid_block_height,fee_lamports,storage_lamports,state) VALUES($1,$2,'fixture','fixture','localnet','fixture',1,0,0,'confirmed')`, post, uuid.NewString())
		if err != nil {
			t.Fatal(err)
		}
		_, err = f.pool.Exec(f.ctx, "UPDATE posts SET status='in_progress' WHERE id=$1", post)
		if err != nil {
			t.Fatal(err)
		}
	}
	fund(f.postID)
	delegated := f.workOp(t, 1, p, work.Request{Action: "delegate", AssignmentID: "patch", CostLamports: 1, EndTime: time.Now().Add(time.Hour)}, 200)
	child := workDecode[struct {
		PostID int64 `json:"post_id"`
	}](t, delegated.Result).PostID
	if child == 0 || child == f.postID {
		t.Fatal("separate child missing")
	}
	f.request(t, "POST", "/posts/info", f.tokens[0], map[string]any{"id": child}, 404)
	feed := f.request(t, "POST", "/posts/feed", f.tokens[0], map[string]any{"level": "easy"}, 200)
	if bytes.Contains(feed, []byte("Private child instructions")) {
		t.Fatal("private child leaked into discovery")
	}
	f.request(t, "GET", fmt.Sprintf("/posts/%d/workspace", child), f.tokens[0], nil, 403)
	f.workOp(t, 1, p, work.Request{Action: "collect", AssignmentID: "patch", SubmissionVersion: 1}, 409)
	_, err := f.pool.Exec(f.ctx, "UPDATE posts SET accepted_by=$2,accepted_at=NOW(),status='negotiating' WHERE id=$1", child, f.users[2])
	if err != nil {
		t.Fatal(err)
	}
	fund(child)
	payload := []byte("exact paid patch")
	sum := sha256.Sum256(payload)
	fileID := uuid.NewString()
	_, err = f.pool.Exec(f.ctx, "INSERT INTO workspace_files(id,post_id,uploaded_by,name,size,sha256,content,purpose) VALUES($1,$2,$3,'patch.diff',$4,$5,$6,'output')", fileID, child, f.users[2], len(payload), hex.EncodeToString(sum[:]), payload)
	if err != nil {
		t.Fatal(err)
	}
	f.request(t, "POST", fmt.Sprintf("/posts/%d/submit", child), f.tokens[2], map[string]any{"note": "verified paid patch", "submission_version": 0, "files": []string{fileID}, "input_files": []string{}}, 201)
	cp := workDecode[work.Contribution](t, f.workOp(t, 1, p, work.Request{Action: "collect", AssignmentID: "patch", SubmissionVersion: 1}, 200).Result)
	if cp.ChildPost != child || cp.ChildVersion != 1 || len(cp.Outputs) != 1 {
		t.Fatal("child delivery provenance missing")
	}
	var childBlocked, parentBlocked bool
	if err = f.pool.QueryRow(f.ctx, "SELECT refund_blocked FROM post_workspaces WHERE post_id=$1", child).Scan(&childBlocked); err != nil || !childBlocked {
		t.Fatal("collecting child output did not protect its refund", err)
	}
	if err = f.pool.QueryRow(f.ctx, "SELECT refund_blocked FROM post_workspaces WHERE post_id=$1", f.postID).Scan(&parentBlocked); err != nil || parentBlocked {
		t.Fatal("child access incorrectly blocked the independent parent refund", err)
	}
	f.workAccept(t, 1, p, "patch", cp.ID)
	integration := workDecode[work.Integration](t, f.workOp(t, 1, p, work.Request{Action: "integrate", Contributions: []string{cp.ID}, Outputs: cp.Outputs, Note: "integrated child patch"}, 200).Result)
	delivery := f.workOp(t, 1, p, work.Request{Action: "deliver", IntegrationID: integration.ID, SubmissionVersion: 0, Inputs: []string{}, Note: "patch and provenance ready"}, 200)
	if !bytes.Contains(delivery.Result, []byte("delivery_files")) {
		t.Fatal("integrated delivery missing")
	}
	var manifest []byte
	err = f.pool.QueryRow(f.ctx, "SELECT content FROM workspace_files WHERE post_id=$1 AND name='maruvo-provenance.json'", f.postID).Scan(&manifest)
	if err != nil || !bytes.Contains(manifest, []byte(cp.ID)) || bytes.Contains(manifest, []byte("unshared-secret-name.txt")) {
		t.Fatal("final delivery lost contribution evidence")
	}
	// Independent authoritative settlement records: settling a child never settles the parent.
	_, err = f.pool.Exec(f.ctx, "UPDATE post_escrows SET state='released' WHERE post_id=$1", child)
	if err != nil {
		t.Fatal(err)
	}
	var parentState string
	err = f.pool.QueryRow(f.ctx, "SELECT state FROM post_escrows WHERE post_id=$1", f.postID).Scan(&parentState)
	if err != nil || parentState != "confirmed" {
		t.Fatal("child release altered parent escrow")
	}
	f.workOp(t, 1, p, work.Request{Action: "control", Mode: "cancelled"}, 200)
	if len(f.workView(t, 1, p).OutstandingChildren) != 0 {
		t.Fatal("settled child reported outstanding")
	}
}
func TestWorkBoundedEventsDuplicateCreateAndRemovedMembership(t *testing.T) {
	f := workFixture(t)
	request := work.Request{Name: "Durable receipts", OperationID: "create-once"}
	one := f.workOp(t, 0, "", request, 200)
	two := f.workOp(t, 0, "", request, 200)
	if one.ProjectID != two.ProjectID || one.Cursor != two.Cursor {
		t.Fatal("create retry made another project")
	}
	p := one.ProjectID
	f.request(t, "GET", "/work/projects/"+p, f.tokens[2], nil, 403)
	f.workTeam(t, 0, p, 1)
	batch := f.request(t, "GET", "/work/projects/"+p+"/events?cursor=999&wait=0", f.tokens[0], nil, 200)
	if !bytes.Contains(batch, []byte(`"resync_required":true`)) {
		t.Fatal("invalid cursor did not request snapshot")
	}
	f.request(t, "GET", "/work/projects/"+p+"/events?wait=6", f.tokens[0], nil, 400)
	f.workOp(t, 0, p, work.Request{Action: "remove", UserID: f.users[1]}, 200)
	f.request(t, "GET", "/work/projects/"+p, f.tokens[1], nil, 403)
	var count int
	if err := f.pool.QueryRow(context.Background(), "SELECT count(*) FROM work_projects WHERE id=$1", p).Scan(&count); err != nil || count != 1 {
		t.Fatal("create receipt mismatch")
	}
}

func TestWorkSimultaneousReceiptRetries(t *testing.T) {
	f := workFixture(t)
	payload, _ := json.Marshal(work.Request{Name: "One committed project", OperationID: "concurrent-create"})
	type response struct {
		status int
		data   []byte
		err    error
	}
	results := make(chan response, 4)
	var group sync.WaitGroup
	for range 4 {
		group.Go(func() {
			req, err := http.NewRequestWithContext(f.ctx, "POST", f.server.URL+"/work/projects", bytes.NewReader(payload))
			if err != nil {
				results <- response{err: err}
				return
			}
			req.Header.Set("Authorization", "Bearer "+f.tokens[0])
			res, err := http.DefaultClient.Do(req)
			if err != nil {
				results <- response{err: err}
				return
			}
			defer res.Body.Close()
			data, err := io.ReadAll(res.Body)
			results <- response{res.StatusCode, data, err}
		})
	}
	group.Wait()
	close(results)
	var original workTestReceipt
	for result := range results {
		if result.err != nil || result.status != 200 {
			t.Fatalf("retry failed: %d %v %s", result.status, result.err, result.data)
		}
		receipt := workDecode[workTestReceipt](t, result.data)
		if original.ProjectID == "" {
			original = receipt
		} else if original.ProjectID != receipt.ProjectID || original.Cursor != receipt.Cursor || !reflect.DeepEqual(workDecode[any](t, original.Result), workDecode[any](t, receipt.Result)) {
			t.Fatal("concurrent retry changed its committed receipt")
		}
	}
	var events int
	if err := f.pool.QueryRow(f.ctx, "SELECT count(*) FROM work_events WHERE project_id=$1", original.ProjectID).Scan(&events); err != nil || events != 1 {
		t.Fatalf("duplicate creation committed %d events: %v", events, err)
	}
}
