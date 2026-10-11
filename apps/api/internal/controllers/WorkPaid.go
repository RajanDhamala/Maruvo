package controller

import (
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	db "github.com/rajandhamala/Maruvo/db/sqlc"
	"github.com/rajandhamala/Maruvo/internal/work"
)

func (c *Controller) workDelegate(r *http.Request, tx pgx.Tx, p *work.Project, a work.Actor, req work.Request) (any, error) {
	if !a.Human() || a.UserID != p.OwnerID || p.Status != "active" {
		return nil, &work.Failure{Status: 403, Message: "lead must authorize each paid child task"}
	}
	s := p.Assignment(req.AssignmentID)
	if s == nil {
		return nil, &work.Failure{Status: 404, Message: "assignment not found"}
	}
	if s.State != "queued" || s.ChildPost != 0 || s.Assignee == p.OwnerID || p.Role(s.Assignee) == "" {
		return nil, &work.Failure{Status: 409, Message: "delegate a queued assignment to another active member"}
	}
	if req.CostLamports <= 0 || !req.EndTime.After(time.Now()) {
		return nil, &work.Failure{Status: 400, Message: "positive cost and future acceptance cutoff required"}
	}
	if err := validateTaskTiming(req.EndTime, req.DeliverBy, req.FundingWindowSeconds, req.ReviewWindowSeconds); err != nil {
		return nil, err
	}
	inputs, deps, err := p.Snapshot(s)
	if err != nil {
		return nil, err
	}
	if s.Kind == "verification" {
		for id := range deps {
			if p.Assignment(id).Assignee == s.Assignee {
				return nil, &work.Failure{Status: 403, Message: "verification needs an independent operator"}
			}
		}
	}
	// Freeze the approved artifact snapshot now, before any child starts work.
	at := work.Attempt{ID: uuid.NewString(), OperatorID: s.Assignee, State: "delegated", BaseRevision: p.Revision, Inputs: inputs, Dependencies: deps, StartedAt: time.Now().UTC()}
	names := []string{}
	seen := map[string]bool{}
	for _, id := range inputs {
		art := p.Artifact(id)
		if art == nil || seen[art.Name] {
			return nil, &work.Failure{Status: 409, Message: "child inputs must have distinct filenames"}
		}
		seen[art.Name] = true
		names = append(names, art.Name)
	}
	var deadline any
	if !req.DeliverBy.IsZero() {
		deadline = req.DeliverBy.UTC()
	}
	err = tx.QueryRow(r.Context(), `INSERT INTO posts(user_id,title,description,acceptance_criteria,cost_lamports,end_time,level,target_worker,visibility,input_files,expected_outputs,funding_window_seconds,deliver_by,review_window_seconds)
 VALUES($1,$2,$3,$4,$5,$6,'easy',$7,'private',$8,$9,$10,$11,$12) RETURNING id`, p.OwnerID, s.Title, s.Instructions, "Lead reviews the exact assignment outputs and verification evidence.", req.CostLamports, req.EndTime, s.Assignee, names, append([]string{}, s.ExpectedOutputs...), req.FundingWindowSeconds, deadline, req.ReviewWindowSeconds).Scan(&s.ChildPost)
	if err != nil {
		return nil, err
	}
	_, err = tx.Exec(r.Context(), "INSERT INTO work_subjobs(post_id,project_id,assignment_id) VALUES($1,$2,$3)", s.ChildPost, p.ID, s.ID)
	if err != nil {
		return nil, err
	}
	// Files become available after the directed worker accepts, through existing task access checks.
	q := db.New(tx)
	post, err := q.GetPost(r.Context(), s.ChildPost)
	if err != nil {
		return nil, err
	}
	// Acceptance creates the workspace, so seed it for inputs without granting participation.
	_, err = tx.Exec(r.Context(), "INSERT INTO post_workspaces(post_id) VALUES($1) ON CONFLICT DO NOTHING", s.ChildPost)
	if err != nil {
		return nil, err
	}
	for _, id := range inputs {
		art := p.Artifact(id)
		var content []byte
		err = tx.QueryRow(r.Context(), "SELECT content FROM work_artifacts WHERE id=$1 AND project_id=$2", id, p.ID).Scan(&content)
		if err != nil {
			return nil, err
		}
		if _, err = saveWorkspaceFile(r.Context(), q, post, p.OwnerID, art.Name, "input", content); err != nil {
			return nil, err
		}
	}
	s.Attempts = append(s.Attempts, at)
	s.State = "blocked"
	s.Blocker = "child acceptance and confirmed funding required"
	s.NextAction = "worker accepts the private task; lead explicitly signs its separate funding"
	p.OutstandingChildren = append(p.OutstandingChildren, s.ChildPost)
	return map[string]any{"post_id": s.ChildPost, "assignment_id": s.ID, "funding_required": true, "parent_payment_unchanged": true}, nil
}
func (c *Controller) workCollect(r *http.Request, tx pgx.Tx, p *work.Project, a work.Actor, req work.Request) (any, error) {
	if !a.Human() || a.UserID != p.OwnerID || p.Status != "active" {
		return nil, &work.Failure{Status: 403, Message: "lead collects child deliveries"}
	}
	s := p.Assignment(req.AssignmentID)
	if s == nil || s.ChildPost == 0 {
		return nil, &work.Failure{Status: 404, Message: "paid assignment not found"}
	}
	var version int64
	var note, state string
	var ids []pgtype.UUID
	err := tx.QueryRow(r.Context(), `SELECT w.submission_version,w.submission,w.review_state,w.delivery_files FROM posts p JOIN post_workspaces w ON w.post_id=p.id JOIN post_escrows e ON e.post_id=p.id WHERE p.id=$1 AND p.accepted_by=$2 AND e.state IN ('confirmed','released') FOR UPDATE OF p,w`, s.ChildPost, s.Assignee).Scan(&version, &note, &state, &ids)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, &work.Failure{Status: 409, Message: "child needs confirmed funding and a delivery"}
	}
	if err != nil {
		return nil, err
	}
	if version != req.SubmissionVersion || (state != "submitted" && state != "approved") {
		return nil, &work.Failure{Status: 409, Message: "child delivery version changed or needs submission"}
	}
	for _, c := range s.Contributions {
		if c.ChildVersion == version && c.ChildPost == s.ChildPost {
			return nil, &work.Failure{Status: 409, Message: "child version already collected"}
		}
	}
	at := s.CurrentAttempt()
	if at == nil {
		return nil, &work.Failure{Status: 409, Message: "child input snapshot is missing"}
	}
	check := work.Contribution{BaseRevision: at.BaseRevision, Dependencies: at.Dependencies}
	if !p.Fresh(&check) {
		return nil, &work.Failure{Status: 409, Message: "child inputs are stale; create a new bounded assignment"}
	}
	if len(p.Artifacts)+len(ids) > 100 {
		return nil, &work.Failure{Status: 409, Message: "project artifact limit reached"}
	}
	outputs := []string{}
	q := db.New(tx)
	post, err := q.GetPost(r.Context(), s.ChildPost)
	if err != nil {
		return nil, err
	}
	for _, id := range ids {
		file, err := q.GetWorkspaceFile(r.Context(), db.GetWorkspaceFileParams{ID: id, PostID: s.ChildPost})
		if err != nil {
			return nil, err
		}
		if file.UploadedBy != s.Assignee {
			return nil, &work.Failure{Status: 409, Message: "child delivery must contain worker files"}
		}
		if err = recordWorkspaceFileAccess(r, q, post, file, a.UserID); err != nil {
			return nil, err
		}
		total := file.Size
		for _, art := range p.Artifacts {
			total += art.Size
		}
		if total > 100<<20 {
			return nil, &work.Failure{Status: 409, Message: "project artifact limit reached"}
		}
		art := work.Artifact{ID: uuid.NewString(), Name: file.Name, SHA256: file.Sha256, Size: file.Size, OwnerID: s.Assignee, AssignmentID: s.ID, AttemptID: at.ID, SourcePost: s.ChildPost, SourceFile: id.String()}
		_, err = tx.Exec(r.Context(), "INSERT INTO work_artifacts(id,project_id,content) VALUES($1,$2,$3)", art.ID, p.ID, file.Content)
		if err != nil {
			return nil, err
		}
		p.Artifacts = append(p.Artifacts, art)
		outputs = append(outputs, art.ID)
	}
	// Apply the same evidence/output contract as an ordinary contribution.
	p.Invalidate(s.ID)
	s.State = "running"
	at.State = "running"
	at.GrantID = ""
	at.LeaseUntil = time.Now().Add(time.Minute)
	submit := work.Request{Action: "submit", AssignmentID: s.ID, AttemptID: at.ID, Inputs: at.Inputs, Outputs: outputs, BaseRevision: at.BaseRevision, Note: note, Evidence: req.Evidence, Usage: req.Usage}
	result, err := p.Apply(work.Actor{UserID: s.Assignee}, submit, time.Now().UTC(), uuid.NewString)
	if err != nil {
		return nil, err
	}
	cptr := &s.Contributions[len(s.Contributions)-1]
	cptr.ChildPost = s.ChildPost
	cptr.ChildVersion = version
	// Append may reallocate, so return the final record, including child provenance.
	_ = result
	return cptr, nil
}
func (c *Controller) workCheckChild(r *http.Request, tx pgx.Tx, s *work.Assignment) error {
	if s == nil || s.ChildPost == 0 {
		return nil
	}
	var version int64
	var state string
	err := tx.QueryRow(r.Context(), "SELECT submission_version,review_state FROM post_workspaces WHERE post_id=$1 FOR UPDATE", s.ChildPost).Scan(&version, &state)
	if err != nil {
		return err
	}
	if len(s.Contributions) == 0 {
		return &work.Failure{Status: 409, Message: "collect the exact child delivery first"}
	}
	cptr := s.Contributions[len(s.Contributions)-1]
	if cptr.ChildVersion != version || (state != "submitted" && state != "approved") {
		return &work.Failure{Status: 409, Message: "child delivery changed; collect the current revision"}
	}
	return nil
}
func (c *Controller) workDeliver(r *http.Request, tx pgx.Tx, p *work.Project, a work.Actor, req work.Request) (any, error) {
	if a.UserID != p.OwnerID || !a.Can("manage") || p.Status != "active" || p.PostID == 0 {
		return nil, &work.Failure{Status: 403, Message: "lead delivers an attached project"}
	}
	var integration *work.Integration
	for i := range p.Integrations {
		if p.Integrations[i].ID == req.IntegrationID {
			integration = &p.Integrations[i]
		}
	}
	if integration == nil || integration.Revision != p.Revision {
		return nil, &work.Failure{Status: 409, Message: "select the latest integration"}
	}
	contributions := []work.Contribution{}
	for _, id := range integration.Contributions {
		found := false
		for i := range p.Assignments {
			s := &p.Assignments[i]
			if s.Accepted == id && s.State == "accepted" {
				cp := s.AcceptedContribution()
				if cp == nil || cp.BaseRevision != integration.BaseRevision {
					return nil, &work.Failure{Status: 409, Message: "integration became stale"}
				}
				for dep, version := range cp.Dependencies {
					if d := p.Assignment(dep); d == nil || d.Accepted != version || d.State != "accepted" {
						return nil, &work.Failure{Status: 409, Message: "verification became stale"}
					}
				}
				if err := c.workCheckChild(r, tx, s); err != nil {
					return nil, err
				}
				contributions = append(contributions, *cp)
				found = true
			}
		}
		if !found {
			return nil, &work.Failure{Status: 409, Message: "integration contribution was replaced"}
		}
	}
	if err := c.workFunding(r, tx, p.PostID); err != nil {
		return nil, err
	}
	q := db.New(tx)
	post, err := q.LockPost(r.Context(), p.PostID)
	if err != nil {
		return nil, err
	}
	if !post.AcceptedBy.Valid || post.AcceptedBy.Int64 != a.UserID {
		return nil, &work.Failure{Status: 403, Message: "only the original worker delivers"}
	}
	ws, err := q.GetWorkspace(r.Context(), p.PostID)
	if err != nil {
		return nil, err
	}
	if ws.SubmissionVersion != req.SubmissionVersion {
		return nil, &work.Failure{Status: 409, Message: "parent delivery changed"}
	}
	settlement, err := q.GetPostSettlement(r.Context(), post.ID)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return nil, err
	}
	if err == nil && settlement.State != "failed" && settlement.State != "expired" {
		return nil, &work.Failure{Status: 409, Message: "parent settlement is active"}
	}
	files, err := q.ListWorkspaceFiles(r.Context(), post.ID)
	if err != nil {
		return nil, err
	}
	inputs, missing := workspaceInputs(post, files)
	if len(missing) > 0 || !slices.Equal(inputs, req.Inputs) {
		return nil, &work.Failure{Status: 409, Message: "parent task inputs changed"}
	}
	// Each parent input used in this integration must still reference that exact requester file.
	for _, cp := range contributions {
		for _, id := range cp.Inputs {
			art := p.Artifact(id)
			if art != nil && art.SourcePost == p.PostID && !slices.Contains(inputs, art.SourceFile) {
				return nil, &work.Failure{Status: 409, Message: "imported requester artifact was replaced"}
			}
		}
	}
	if len(integration.Outputs) >= 20 {
		return nil, &work.Failure{Status: 400, Message: "leave a delivery slot for maruvo-provenance.json"}
	}
	delivery := []pgtype.UUID{}
	names := map[string]bool{}
	for _, id := range integration.Outputs {
		art := p.Artifact(id)
		if art == nil || names[art.Name] || art.Name == "maruvo-provenance.json" {
			return nil, &work.Failure{Status: 400, Message: "integration output filenames must be distinct"}
		}
		names[art.Name] = true
		var content []byte
		err = tx.QueryRow(r.Context(), "SELECT content FROM work_artifacts WHERE id=$1 AND project_id=$2", id, p.ID).Scan(&content)
		if err != nil {
			return nil, err
		}
		file, e := saveWorkspaceFile(r.Context(), q, post, p.OwnerID, art.Name, "output", content)
		if e != nil {
			return nil, e
		}
		delivery = append(delivery, file.ID)
	}
	for _, name := range post.ExpectedOutputs {
		if !names[name] {
			return nil, &work.Failure{Status: 409, Message: "integration is missing parent output " + name}
		}
	}
	referenced := map[string]bool{}
	for _, cp := range contributions {
		for _, id := range append(append([]string{}, cp.Inputs...), cp.Outputs...) {
			referenced[id] = true
		}
	}
	artifacts := []work.Artifact{}
	for _, art := range p.Artifacts {
		if referenced[art.ID] {
			artifacts = append(artifacts, art)
		}
	}
	manifest, err := json.Marshal(map[string]any{"project_id": p.ID, "integration": integration, "contributions": contributions, "artifacts": artifacts})
	if err != nil {
		return nil, err
	}
	provenance, err := saveWorkspaceFile(r.Context(), q, post, p.OwnerID, "maruvo-provenance.json", "output", manifest)
	if err != nil {
		return nil, err
	}
	delivery = append(delivery, provenance.ID)
	note := req.Note
	if note == "" {
		note = integration.Note
	}
	if len(note) == 0 || !validTaskText(note, 4000, 16000) {
		return nil, &work.Failure{Status: 400, Message: "provide a delivery note"}
	}
	ws, err = q.SubmitWorkspace(r.Context(), db.SubmitWorkspaceParams{PostID: post.ID, Submission: note, DeliveryFiles: delivery})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, &work.Failure{Status: 409, Message: "parent already submitted"}
	}
	if err != nil {
		return nil, err
	}
	_, err = appendEvent(r, q, post.ID, p.OwnerID, "work.submitted", map[string]any{"note": note, "submission_version": ws.SubmissionVersion, "delivery_files": delivery, "project_id": p.ID, "integration_id": integration.ID, "input_file_ids": inputs, "inputs_verified": true})
	return map[string]any{"post_id": post.ID, "submission_version": ws.SubmissionVersion, "delivery_files": delivery, "integration_id": integration.ID, "review_state": ws.ReviewState}, err
}
