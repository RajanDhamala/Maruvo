package controller

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	db "github.com/rajandhamala/Maruvo/db/sqlc"
	"github.com/rajandhamala/Maruvo/internal/utils"
)

func TestWorkspaceInputSnapshot(t *testing.T) {
	file := func(name, purpose string, owner int64) db.ListWorkspaceFilesRow {
		return db.ListWorkspaceFilesRow{
			ID: pgtype.UUID{Bytes: uuid.New(), Valid: true}, Name: name, Purpose: purpose, UploadedBy: owner,
		}
	}
	old := file("source.go", "input", 1)
	spec := file("spec.txt", "shared", 1)
	current := file("source.go", "shared", 1)
	files := []db.ListWorkspaceFilesRow{old, spec, current, file("source.go", "output", 2)}
	post := db.Post{UserID: 1, InputFiles: []string{"spec.txt", "source.go", "absent.txt"}}

	ids, missing := workspaceInputs(post, files)
	if !slices.Equal(ids, []string{spec.ID.String(), current.ID.String()}) ||
		!slices.Equal(missing, []string{"absent.txt"}) {
		t.Fatalf("declared snapshot used wrong files/order: %v %v", ids, missing)
	}

	post.InputFiles = nil

	ids, missing = workspaceInputs(post, files)
	if !slices.Equal(ids, []string{current.ID.String(), spec.ID.String()}) || len(missing) != 0 {
		t.Fatalf("undeclared inputs must use latest requester files sorted by name: %v %v", ids, missing)
	}
}

func TestWorkspaceContextPermissionAndSettlement(t *testing.T) {
	post := db.Post{
		UserID:     1,
		AcceptedBy: pgtype.Int8{Int64: 2, Valid: true},
		Status:     db.PostStatusInProgress,
	}
	state := db.PostWorkspace{ReviewState: "submitted", SubmissionVersion: 3}
	ctx := context.WithValue(context.Background(), utils.AgentKey,
		&utils.AgentAccess{Permissions: []string{"read"}})

	reviewer := taskWorkspaceContext(
		ctx,
		post,
		state,
		nil,
		fundingView{State: "confirmed"},
		settlementView{},
		1,
		true,
	)
	if reviewer.Role != "requester" || reviewer.BlockedActions["request-changes"] != "permission_required" ||
		!reviewer.HumanPaymentRequired || !slices.Contains(reviewer.AllowedActions, "history") {
		t.Fatalf("read-only grant advertised reviewer mutation: %+v", reviewer)
	}

	reviewer = taskWorkspaceContext(context.Background(), post, state, nil,
		fundingView{State: "confirmed"}, settlementView{State: "pending"}, 3, true)
	if reviewer.Role != "reviewer" || reviewer.BlockedActions["request-changes"] != "settlement" ||
		reviewer.HumanPaymentRequired || !slices.Equal(reviewer.WaitingFor, []string{"settlement"}) {
		t.Fatalf("pending settlement must block a new review/signature: %+v", reviewer)
	}

	reviewer = taskWorkspaceContext(context.Background(), post, state, nil,
		fundingView{State: "refunded"}, settlementView{}, 3, true)
	if !reviewer.Terminal || len(reviewer.WaitingFor) != 0 || reviewer.HumanPaymentRequired ||
		reviewer.BlockedActions["message"] != "task_closed" {
		t.Fatalf("settled task advertised active work: %+v", reviewer)
	}
}

func TestWorkspaceDelegationContext(t *testing.T) {
	f := newAgentAccessFixture(t)
	path := fmt.Sprintf("/posts/%d", f.postID)
	worker, _ := f.grant(t, 1, "read", "upload", "submit")

	read, _ := f.grant(t, 1, "read")
	if _, err := f.pool.Exec(
		f.ctx,
		`UPDATE posts SET input_files=ARRAY['spec.txt'], expected_outputs=ARRAY['result.txt'],
		end_time=NOW()-INTERVAL '2 hours', funding_window_seconds=60, review_window_seconds=60,
		deliver_by=NOW()-INTERVAL '1 minute' WHERE id=$1`,
		f.postID,
	); err != nil {
		t.Fatal(err)
	}

	load := func(token string) workspaceContext {
		t.Helper()

		var result struct {
			Context workspaceContext `json:"context"`
		}
		if err := json.Unmarshal(
			f.request(t, "GET", path+"/workspace", token, nil, 200),
			&result,
		); err != nil {
			t.Fatal(err)
		}

		return result.Context
	}

	blocked := load(worker)
	if blocked.Role != "worker" || blocked.BlockedActions["submit"] != "funding" ||
		!slices.Equal(blocked.WaitingFor, []string{"funding", "inputs"}) ||
		!slices.Equal(blocked.MissingInputs, []string{"spec.txt"}) ||
		slices.Contains(blocked.UploadPurposes, "output") || blocked.HumanPaymentRequired {
		t.Fatalf("unfunded worker context was not actionable: %+v", blocked)
	}

	if requester := load(f.tokens[0]); !requester.HumanPaymentRequired || requester.Role != "requester" {
		t.Fatalf("requester was not told to authorize funding: %+v", requester)
	}

	if readonly := load(read); readonly.BlockedActions["submit"] != "permission_required" ||
		readonly.BlockedActions["upload"] != "permission_required" || len(readonly.UploadPurposes) != 0 {
		t.Fatalf("context ignored grant permissions: %+v", readonly)
	}

	reviewerWallet := uuid.NewString()
	if _, err := f.pool.Exec(
		f.ctx,
		"INSERT INTO wallets(user_id,address) VALUES($1,$2)",
		f.users[0],
		reviewerWallet,
	); err != nil {
		t.Fatal(err)
	}

	if _, err := f.pool.Exec(
		f.ctx,
		`INSERT INTO post_escrows(post_id,address,program_id,reviewer,network,transaction,
		last_valid_block_height,fee_lamports,storage_lamports,state)
		VALUES($1,$2,'fixture',$3,'localnet','fixture',1,0,0,'confirmed')`,
		f.postID,
		uuid.NewString(),
		reviewerWallet,
	); err != nil {
		t.Fatal(err)
	}

	if _, err := f.pool.Exec(
		f.ctx,
		"UPDATE posts SET status='in_progress' WHERE id=$1",
		f.postID,
	); err != nil {
		t.Fatal(err)
	}

	save := func(owner int, name, purpose string) string {
		t.Helper()

		post, err := f.controller.queries.GetPost(f.ctx, f.postID)
		if err != nil {
			t.Fatal(err)
		}

		file, err := saveWorkspaceFile(
			f.ctx,
			f.controller.queries,
			post,
			f.users[owner],
			name,
			purpose,
			[]byte("fixture"),
		)
		if err != nil {
			t.Fatal(err)
		}

		return file.ID.String()
	}

	if blocked = load(worker); blocked.BlockedActions["submit"] != "inputs" {
		t.Fatalf("funded task with missing inputs: %+v", blocked)
	}

	oldInput := save(0, "spec.txt", "input")

	input := save(0, "spec.txt", "shared")
	if blocked = load(worker); blocked.BlockedActions["submit"] != "outputs" ||
		!slices.Equal(blocked.InputFileIDs, []string{input}) {
		t.Fatalf("latest input or missing-output gate was incorrect: %+v", blocked)
	}

	output := save(1, "result.txt", "output")

	ready := load(worker)
	if !slices.Contains(ready.AllowedActions, "submit") || len(ready.WaitingFor) != 0 ||
		len(ready.MissingOutputs) != 0 || ready.SubmissionVersion != 0 {
		t.Fatalf("overdue but funded task should remain deliverable: %+v", ready)
	}

	payload := map[string]any{"note": "Ready for review", "submission_version": 0,
		"files": []string{output}, "input_files": []string{oldInput}}
	f.request(t, "POST", path+"/submit", worker, payload, 409)
	payload["input_files"] = []string{input}
	f.request(t, "POST", path+"/submit", worker, payload, 201)

	if submitted := load(
		worker,
	); submitted.SubmissionVersion != 1 ||
		submitted.BlockedActions["submit"] != "review" ||
		!slices.Equal(submitted.WaitingFor, []string{"review"}) {
		t.Fatalf("submitted worker did not wait for review: %+v", submitted)
	}

	if reviewer := load(f.tokens[0]); !reviewer.HumanPaymentRequired ||
		!slices.Contains(reviewer.AllowedActions, "request-changes") {
		t.Fatalf("reviewer context omitted review/payment: %+v", reviewer)
	}

	var page struct {
		Events []db.WorkspaceEvent `json:"events"`
	}
	if err := json.Unmarshal(f.request(t, "GET", path+"/history", worker, nil, 200), &page); err != nil {
		t.Fatal(err)
	}

	verified := false

	for _, event := range page.Events {
		if event.Kind != "work.submitted" {
			continue
		}

		var provenance struct {
			Inputs   []string `json:"input_file_ids"`
			Verified bool     `json:"inputs_verified"`
		}
		if err := json.Unmarshal(event.Data, &provenance); err != nil {
			t.Fatal(err)
		}

		verified = provenance.Verified && slices.Equal(provenance.Inputs, []string{input}) &&
			event.ActorID.Int64 == f.users[1]
	}

	if !verified {
		t.Fatal("durable delivery history lost the checked input snapshot")
	}

	f.request(t, "POST", path+"/review/changes", f.tokens[0],
		map[string]any{"submission_version": 1, "note": "Update the result"}, 201)

	if revision := load(worker); revision.BlockedActions["submit"] != "outputs" ||
		!slices.Equal(revision.MissingOutputs, []string{"result.txt"}) || revision.SubmissionVersion != 1 {
		t.Fatalf("revision reused a previous delivery: %+v", revision)
	}

	save(1, "result.txt", "output")

	if revision := load(worker); !slices.Contains(revision.AllowedActions, "submit") {
		t.Fatalf("fresh revision output did not unblock delivery: %+v", revision)
	}

	f.request(t, "POST", path+"/submit", worker, map[string]string{"note": "Legacy revision"}, 201)

	if err := json.Unmarshal(f.request(t, "GET", path+"/history", worker, nil, 200), &page); err != nil {
		t.Fatal(err)
	}

	for _, event := range page.Events {
		if event.Kind != "work.submitted" {
			continue
		}

		var provenance struct {
			Version  int64    `json:"submission_version"`
			Inputs   []string `json:"input_file_ids"`
			Verified bool     `json:"inputs_verified"`
		}
		if err := json.Unmarshal(event.Data, &provenance); err != nil {
			t.Fatal(err)
		}

		if provenance.Version == 2 && (provenance.Verified || provenance.Inputs != nil) {
			t.Fatal("legacy submission incorrectly claimed a verified input snapshot")
		}
	}
}

func TestWorkspaceHistoryPagination(t *testing.T) {
	f := newAgentAccessFixture(t)
	path := fmt.Sprintf("/posts/%d/history", f.postID)
	read, grantID := f.grant(t, 1, "read")

	appendEvent := func(n int) int64 {
		t.Helper()

		data, _ := json.Marshal(map[string]any{"text": fmt.Sprint(n), "agent_grant_id": grantID})

		id, err := f.controller.queries.AppendWorkspaceEvent(f.ctx, db.AppendWorkspaceEventParams{
			PostID:  f.postID,
			ActorID: pgtype.Int8{Int64: f.users[1], Valid: true},
			Kind:    "message",
			Data:    data,
		})
		if err != nil {
			t.Fatal(err)
		}

		return id
	}
	for n := range 145 {
		appendEvent(n)
	}

	var total int
	if err := f.pool.QueryRow(f.ctx, "SELECT COUNT(*) FROM workspace_events WHERE post_id=$1", f.postID).
		Scan(&total); err != nil {
		t.Fatal(err)
	}

	seen := map[int64]bool{}
	before := int64(0)

	for pageNumber := 0; ; pageNumber++ {
		var page struct {
			PostID     int64               `json:"post_id"`
			Events     []db.WorkspaceEvent `json:"events"`
			HasMore    bool                `json:"has_more"`
			NextBefore int64               `json:"next_before"`
		}

		body := f.request(t, "GET", fmt.Sprintf("%s?before=%d&limit=37", path, before), read, nil, 200)
		if err := json.Unmarshal(body, &page); err != nil {
			t.Fatal(err)
		}

		if page.PostID != f.postID || len(page.Events) == 0 || len(page.Events) > 37 {
			t.Fatalf("bad history page: %s", body)
		}

		for i, event := range page.Events {
			if seen[event.ID] || (before > 0 && event.ID >= before) ||
				(i > 0 && event.ID <= page.Events[i-1].ID) {
				t.Fatalf("history duplicated/reordered events across pages: %s", body)
			}

			seen[event.ID] = true
			if event.Kind == "message" && (event.ActorID.Int64 != f.users[1] || len(event.Data) == 0) {
				t.Fatal("history lost actor/provenance")
			}
		}

		if pageNumber == 0 {
			appendEvent(999)
		}

		if !page.HasMore {
			if page.NextBefore != 0 {
				t.Fatal("last page did not terminate")
			}

			break
		}

		if page.NextBefore != page.Events[0].ID {
			t.Fatal("page omitted its exclusive continuation")
		}

		before = page.NextBefore
	}

	if len(seen) != total {
		t.Fatalf("paging lost old events: got %d, want %d", len(seen), total)
	}

	for _, query := range []string{"?before=-1", "?before=oops", "?limit=0", "?limit=101"} {
		f.request(t, "GET", path+query, read, nil, 400)
	}

	f.request(t, "GET", path, f.tokens[2], nil, 403)
	f.request(t, "GET", fmt.Sprintf("/posts/%d/history", f.postID+100000), read, nil, 403)

	if _, err := f.pool.Exec(f.ctx, "UPDATE posts SET status='completed' WHERE id=$1", f.postID); err != nil {
		t.Fatal(err)
	}

	f.request(t, "GET", path, read, nil, 200)
	f.request(t, "POST", "/agents/"+grantID+"/revoke", f.tokens[1], nil, 200)
	f.request(t, "GET", path, read, nil, 401)
}
